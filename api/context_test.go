package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mostlygeek/go-syncstorage/syncstorage"
	"github.com/stretchr/testify/assert"
)

var (
	collectionNames = []string{
		"bookmarks",
		"history",
		"forms",
		"prefs",
		"tabs",
		"passwords",
		"crypto",
		"client",
		"keys",
		"meta",
	}
)

// used for testing that the returned json data is good
type jsResult []jsonBSO
type jsonBSO struct {
	Id        string  `json:"id"`
	Modified  float64 `json:"modified"`
	Payload   string  `json:"payload"`
	SortIndex int     `json:"sortindex"`
}

func init() {
	rand.Seed(time.Now().UnixNano())
}

func makeTestContext() *Context {
	dir, _ := ioutil.TempDir(os.TempDir(), "sync_storage_api_test")
	dispatch, err := syncstorage.NewDispatch(4, dir, syncstorage.TwoLevelPath, 10)
	if err != nil {
		panic(err)
	}

	context, err := NewContext([]string{"sekret"}, dispatch)
	if err != nil {
		panic(err)
	}
	context.DisableHawk = true
	return context
}

// at some point in life it gets tiring to keep typing the same
// string concatination over and over...
func syncurl(uid interface{}, path string) string {
	var u string

	switch uid.(type) {
	case string:
		u = uid.(string)
	case uint64:
		u = strconv.FormatUint(uid.(uint64), 10)
	case int:
		u = strconv.Itoa(uid.(int))
	default:
		panic("Unknown uid type")
	}

	return "http://synchost/1.5/" + u + "/" + path
}

func request(method, urlStr string, body io.Reader, c *Context) *httptest.ResponseRecorder {
	header := make(http.Header)
	header.Set("Accept", "application/json")
	return requestheaders(method, urlStr, body, header, c)
}

func jsonrequest(method, urlStr string, body io.Reader, c *Context) *httptest.ResponseRecorder {
	header := make(http.Header)
	header.Set("Accept", "application/json")
	header.Set("Content-Type", "application/json")
	return requestheaders(method, urlStr, body, header, c)
}

func requestheaders(method, urlStr string, body io.Reader, header http.Header, c *Context) *httptest.ResponseRecorder {

	req, err := http.NewRequest(method, urlStr, body)
	req.Header = header

	if err != nil {
		panic(err)
	}

	return sendrequest(req, c)
}

func sendrequest(req *http.Request, c *Context) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	if c == nil {
		c = makeTestContext()
	}

	router := NewRouterFromContext(c)
	router.ServeHTTP(w, req)
	return w
}

func TestContextWeave404(t *testing.T) {
	assert := assert.New(t)
	resp := request("GET", "/nonexistant/url", nil, nil)
	assert.Equal(resp.Code, http.StatusNotFound)
	assert.Equal(resp.Header().Get("Content-Type"), "application/json")
	assert.Equal(resp.Body.String(), "0") // well that is json
}

func TestContextHeartbeat(t *testing.T) {
	resp := request("GET", "/__heartbeat__", nil, nil)
	assert.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, "OK", resp.Body.String())
}

func TestContextHasXWeaveTimestamp(t *testing.T) {
	resp := request("GET", "/__heartbeat__", nil, nil)
	assert.Equal(t, http.StatusOK, resp.Code)
	assert.NotEqual(t, "", resp.Header().Get("X-Weave-Timestamp"))
}

func TestContextEchoUID(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	resp := request("GET", "/1.5/123456/echo-uid", nil, nil)

	assert.NotEqual("", resp.Header().Get("X-Weave-Timestamp"))
	assert.NotEqual("", resp.Header().Get("X-Last-Modified"))
	assert.Equal(http.StatusOK, resp.Code)
	assert.Equal("123456", resp.Body.String())
}

// TestContextXWeaveTimestamp makes sure header exists
// and is set to the correct value
func TestContextXWeaveTimestamp(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	resp := request("GET", "/1.5/123456/echo-uid", nil, nil)

	ts := resp.Header().Get("X-Weave-Timestamp")
	lm := resp.Header().Get("X-Last-Modified")

	assert.NotEqual(ts, "")
	assert.NotEqual(lm, "")
	assert.Equal(ts, lm)
}

func TestContextInfoQuota(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	context := makeTestContext()

	uid := "123456"

	{ // put some data in..
		body := bytes.NewBufferString(`[
		{"id":"bso1", "payload": "initial payload", "sortindex": 1, "ttl": 2100000},
		{"id":"bso2", "payload": "initial payload", "sortindex": 1, "ttl": 2100000},
		{"id":"bso3", "payload": "initial payload", "sortindex": 1, "ttl": 2100000} ]`)

		req, _ := http.NewRequest("POST", "/1.5/"+uid+"/storage/col2", body)
		req.Header.Add("Content-Type", "application/json")

		resp := sendrequest(req, context)
		if !assert.Equal(http.StatusOK, resp.Code) {
			return
		}
	}

	{
		resp := request("GET", "http://test/1.5/"+uid+"/info/quota", nil, context)
		assert.Equal("[0.0439453125,null]", resp.Body.String())
	}

}

func TestContextInfoCollections(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	context := makeTestContext()

	uid := "123456"
	modified := syncstorage.Now()
	expected := map[string]int{
		"bookmarks": modified,
		"history":   modified + 1000,
		"forms":     modified + 2000,
		"prefs":     modified + 3000,
		"tabs":      modified + 4000,
		"passwords": modified + 5000,
		"crypto":    modified + 6000,
		"client":    modified + 7000,
		"keys":      modified + 8000,
		"meta":      modified + 9000,
	}

	for cName, modified := range expected {
		cId, err := context.Dispatch.GetCollectionId(uid, cName)
		if !assert.NoError(err, "%v", err) {
			return
		}
		err = context.Dispatch.TouchCollection(uid, cId, modified)
		if !assert.NoError(err, "%v", err) {
			return
		}
	}

	resp := request("GET", "http://test/1.5/"+uid+"/info/collections", nil, context)

	if !assert.Equal(http.StatusOK, resp.Code) {
		return
	}

	data := resp.Body.Bytes()
	var collections map[string]int
	err := json.Unmarshal(data, &collections)
	if !assert.NoError(err) {
		return
	}

	for cName, expectedTs := range expected {
		ts, ok := collections[cName]
		if assert.True(ok, "expected '%s' collection to be set", cName) {
			assert.Equal(expectedTs, ts)
		}
	}

	// Test X-If-Modified-Since
	{
		header := make(http.Header)

		// use the oldest timestamp
		header.Add("X-If-Modified-Since", syncstorage.ModifiedToString(expected["meta"]))
		header.Add("Accept", "application/json")
		resp := requestheaders(
			"GET",
			"http://test/1.5/"+uid+"/info/collections",
			nil,
			header,
			context)

		assert.Equal(http.StatusNotModified, resp.Code)
	}

	// Test X-If-Unmodified-Since
	{
		header := make(http.Header)
		header.Add("X-If-Unmodified-Since", syncstorage.ModifiedToString(expected["keys"]))
		header.Add("Accept", "application/json")
		resp := requestheaders(
			"GET",
			"http://test/1.5/"+uid+"/info/collections",
			nil,
			header,
			context)

		assert.Equal(http.StatusPreconditionFailed, resp.Code)
	}

	// Test X-I-M-S with a bad value
	{
		header := make(http.Header)
		header.Add("X-If-Modified-Since", "-1.0")
		header.Add("Accept", "application/json")
		resp := requestheaders(
			"GET",
			"http://test/1.5/"+uid+"/info/collections",
			nil,
			header,
			context)

		assert.Equal(http.StatusBadRequest, resp.Code)
	}

	// Test X-I-U-S with a bad value
	{
		header := make(http.Header)
		header.Add("X-If-Unmodified-Since", "-1.0")
		header.Add("Accept", "application/json")
		resp := requestheaders(
			"GET",
			"http://test/1.5/"+uid+"/info/collections",
			nil,
			header,
			context)

		assert.Equal(http.StatusBadRequest, resp.Code)
	}

}

func TestContextInfoCollectionUsage(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)

	uid := "12345"
	context := makeTestContext()

	sizes := []int{463, 467, 479, 487, 491}

	for _, cName := range collectionNames {
		cId, err := context.Dispatch.GetCollectionId(uid, cName)
		if !assert.NoError(err, "getting cID: %v", err) {
			return
		}

		for id, size := range sizes {
			payload := strings.Repeat("x", size)
			bId := fmt.Sprintf("bid_%d", id)
			_, err = context.Dispatch.PutBSO(uid, cId, bId, &payload, nil, nil)
			if !assert.NoError(err, "failed PUT into %s, bid(%s): %v", cName, bId, err) {
				return
			}
		}
	}

	resp := request("GET", "http://test/1.5/"+uid+"/info/collection_usage", nil, context)
	data := resp.Body.Bytes()

	var collections map[string]float64
	err := json.Unmarshal(data, &collections)
	if !assert.NoError(err) {
		return
	}

	var expectedKB float64
	for _, s := range sizes {
		expectedKB += float64(s) / 1024
	}

	for _, cName := range collectionNames {
		assert.Equal(expectedKB, collections[cName])
	}
}

func TestContextCollectionCounts(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)

	uid := "12345"
	context := makeTestContext()

	expected := make(map[string]int)

	for _, cName := range collectionNames {
		expected[cName] = 5 + rand.Intn(25)
	}

	for cName, numBSOs := range expected {
		cId, err := context.Dispatch.GetCollectionId(uid, cName)
		if !assert.NoError(err, "getting cID: %v", err) {
			return
		}

		payload := "hello"
		for i := 0; i < numBSOs; i++ {
			bId := fmt.Sprintf("bid%d", i)
			_, err = context.Dispatch.PutBSO(uid, cId, bId, &payload, nil, nil)
			if !assert.NoError(err, "failed PUT into %s, bid(%s): %v", cName, bId, err) {
				return
			}
		}
	}

	resp := request("GET", "http://test/1.5/"+uid+"/info/collection_counts", nil, context)
	data := resp.Body.Bytes()

	var collections map[string]int
	err := json.Unmarshal(data, &collections)
	if !assert.NoError(err) {
		return
	}

	for cName, expectedCount := range expected {
		assert.Equal(expectedCount, collections[cName])
	}
}

func TestContextCollectionGET(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	context := makeTestContext()
	uid := "123456"

	// serverside limiting of the max requests!
	context.MaxBSOGetLimit = 4

	// MAKE SOME TEST DATA
	cId, _ := context.Dispatch.GetCollectionId(uid, "bookmarks")
	payload := "some data"
	numBSOsToMake := 5

	for i := 0; i < numBSOsToMake; i++ {
		bId := "bid_" + strconv.Itoa(i)
		sortindex := i
		_, err := context.Dispatch.PutBSO(uid, cId, bId, &payload, &sortindex, nil)

		// sleep a bit so we get some spacing between bso modified times
		// a doublel digit sleep is required since we're only accurate
		// to the 100th of a millisecond ala sync1.5 api
		time.Sleep(19 * time.Millisecond)
		if !assert.NoError(err) {
			return
		}
	}

	base := "http://test/1.5/" + uid + "/storage/bookmarks"

	// Without `full` just the bsoIds are returned
	{
		resp := request("GET", base+`?sort=newest`, nil, context)
		if !assert.Equal(http.StatusOK, resp.Code) {
			return
		}
		assert.Equal(`["bid_4","bid_3","bid_2","bid_1"]`, resp.Body.String())
	}

	// test different sort order
	{
		resp := request("GET", base+`?sort=oldest`, nil, context)
		if !assert.Equal(http.StatusOK, resp.Code) {
			return
		}
		assert.Equal(`["bid_0","bid_1","bid_2","bid_3"]`, resp.Body.String())
	}

	// test full param
	{
		resp := request("GET", base+"?ids=bid_0,bid_1&full=yes&sort=oldest", nil, context)
		if !assert.Equal(http.StatusOK, resp.Code) {
			return
		}

		body := resp.Body.Bytes()
		var results jsResult

		if assert.NoError(json.Unmarshal(body, &results), "JSON Decode error") {
			assert.Len(results, 2)
			assert.Equal("bid_0", results[0].Id)
			assert.Equal("bid_1", results[1].Id)

			assert.Equal(payload, results[0].Payload)
			assert.Equal(payload, results[1].Payload)
		}
	}

	// test limit+offset works
	{
		resp := request("GET", base+`?sort=oldest&limit=2`, nil, context)
		if !assert.Equal(http.StatusOK, resp.Code) {
			return
		}
		assert.Equal(`["bid_0","bid_1"]`, resp.Body.String())

		offset := resp.Header().Get("X-Weave-Next-Offset")
		if !assert.Equal("2", offset) {
			return
		}

		resp2 := request("GET", base+`?sort=oldest&limit=2&offset=`+offset, nil, context)
		if !assert.Equal(http.StatusOK, resp2.Code) {
			return
		}
		assert.Equal(`["bid_2","bid_3"]`, resp2.Body.String())
	}

	// test automatic max offset. An artificially small MaxBSOGetLimit is defined
	// in the context to make sure this behaves as expected
	{
		// Get everything but make sure the `limit` we have works
		resp := request("GET", base+`?full=yes&sort=newest`, nil, context)
		if !assert.Equal(http.StatusOK, resp.Code) {
			return
		}

		body := resp.Body.Bytes()
		var results jsResult

		if assert.NoError(json.Unmarshal(body, &results), "JSON Decode error") {

			assert.Len(results, context.MaxBSOGetLimit)

			// make sure sort=oldest works
			assert.Equal("bid_4", results[0].Id)
			assert.Equal(payload, results[0].Payload)
			assert.Equal("4", resp.Header().Get("X-Weave-Next-Offset"))
		}
	}

	// Test newer param
	{
		for i := 0; i < numBSOsToMake-1; i++ {
			id := strconv.Itoa(i)
			idexpected := strconv.Itoa(i + 1)

			// Get everything but make sure the `limit` we have works
			resp := request("GET", base+"?full=yes&ids=bid_"+id, nil, context)
			if !assert.Equal(http.StatusOK, resp.Code) {
				return
			}

			body := resp.Body.Bytes()
			var results jsResult

			if assert.NoError(json.Unmarshal(body, &results), "JSON Decode error") {
				if !assert.Len(results, 1) {
					return
				}

				modified := fmt.Sprintf("%.02f", results[0].Modified)
				url := base + "?full=yes&limit=1&sort=oldest&newer=" + modified

				resp2 := request("GET", url, nil, context)
				if !assert.Equal(http.StatusOK, resp2.Code) {
					return
				}

				body2 := resp2.Body.Bytes()
				var results2 jsResult
				if assert.NoError(json.Unmarshal(body2, &results2), "JSON Decode error") {
					if !assert.Len(results2, 1) {
						return
					}
					if !assert.Equal("bid_"+idexpected, results2[0].Id, "modified timestamp precision error?") {
						return
					}
				}

			}
		}
	}

	// test non existant collection returns an empty list
	{
		url := "http://test/1.5/" + uid + "/storage/this_is_not_a_real_collection"
		resp := request("GET", url, nil, context)

		assert.Equal(http.StatusOK, resp.Code)
		assert.Equal("[]", resp.Body.String())
	}

}

func TestContextCollectionGETValidatesData(t *testing.T) {

	t.Parallel()
	assert := assert.New(t)
	uid := "1234"

	base := "http://test/1.5/" + uid + "/storage/bookmarks?"
	reqs := map[string]int{
		base + "ids=":                        200,
		base + "ids=abd,123,456":             200,
		base + "ids=no\ttabs\tallowed, here": 400,

		base + "newer=":      200,
		base + "newer=1004":  200,
		base + "newer=-1":    400,
		base + "newer=abcde": 400,

		base + "full=ok": 200,
		base + "full=":   200,

		base + "limit=":    200,
		base + "limit=123": 200,
		base + "limit=a":   400,
		base + "limit=0":   400,
		base + "limit=-1":  400,

		base + "offset=":    200,
		base + "offset=0":   200,
		base + "offset=123": 200,
		base + "offset=a":   400,
		base + "offset=-1":  400,

		base + "sort=":        200,
		base + "sort=newest":  200,
		base + "sort=oldest":  200,
		base + "sort=index":   200,
		base + "sort=invalid": 400,
	}

	// reuse a single context to not a make a bunch
	// of new storage sub-dirs in testing
	context := makeTestContext()

	for url, expected := range reqs {
		resp := request("GET", url, nil, context)
		assert.Equal(expected, resp.Code, fmt.Sprintf("url:%s => %s", url, resp.Body.String()))
	}
}

func TestParseIntoBSO(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)

	{
		var b syncstorage.PutBSOInput
		j := []byte(`{"id":"bso1", "payload": "payload", "sortindex": 1, "ttl": 2100000}`)
		assert.Nil(parseIntoBSO(j, &b))
	}

	{
		var b syncstorage.PutBSOInput
		j := []byte(`{invalid json}`)
		e := parseIntoBSO(j, &b)
		if assert.NotNil(e) {
			assert.Equal("-", e.field)
		}
	}

	{
		var b syncstorage.PutBSOInput
		j := []byte(`{"id": 123, "payload": "payload", "sortindex": 1, "ttl": 2100000}`)
		e := parseIntoBSO(j, &b)
		if assert.NotNil(e) {
			assert.Equal("", e.bId)
			assert.Equal("id", e.field)
		}
	}

	{
		var b syncstorage.PutBSOInput
		j := []byte(`{"id":"bso1", "payload": 1234, "sortindex": 1, "ttl": 2100000}`)
		e := parseIntoBSO(j, &b)
		if assert.NotNil(e) {
			assert.Equal("payload", e.field)
		}
	}

	{
		var b syncstorage.PutBSOInput
		j := []byte(`{"id":"bso1", "payload": "payload", "sortindex": "meh", "ttl": 2100000}`)
		e := parseIntoBSO(j, &b)
		if assert.NotNil(e) {
			assert.Equal("sortindex", e.field)
		}
	}

	{
		var b syncstorage.PutBSOInput
		j := []byte(`{"id":"bso1", "payload": "payload", "sortindex": "1", "ttl": "eh"}`)
		e := parseIntoBSO(j, &b)
		if assert.NotNil(e) {
			assert.Equal("ttl", e.field)
		}
	}
}

func TestContextCollectionPOST(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	context := makeTestContext()

	uid := "123456"

	// Make sure INSERT works first
	body := bytes.NewBufferString(`[
		{"id":"bso1", "payload": "initial payload", "sortindex": 1, "ttl": 2100000},
		{"id":"bso2", "payload": "initial payload", "sortindex": 1, "ttl": 2100000},
		{"id":"bso3", "payload": "initial payload", "sortindex": 1, "ttl": 2100000}
	]`)

	{
		req, _ := http.NewRequest("POST", "/1.5/"+uid+"/storage/bookmarks", nil)
		req.Header.Add("Content-Type", "application/octet-stream")
		resp := sendrequest(req, context)
		if !assert.Equal(http.StatusUnsupportedMediaType, resp.Code) {
			return
		}
	}

	req, _ := http.NewRequest("POST", "/1.5/"+uid+"/storage/bookmarks", body)
	req.Header.Add("Content-Type", "application/json")

	resp := sendrequest(req, context)
	assert.Equal(http.StatusOK, resp.Code)

	var results PostResults
	jsbody := resp.Body.Bytes()
	err := json.Unmarshal(jsbody, &results)
	if !assert.NoError(err) {
		return
	}

	assert.Len(results.Success, 3)
	assert.Len(results.Failed, 0)

	cId, _ := context.Dispatch.GetCollectionId(uid, "bookmarks")
	for _, bId := range []string{"bso1", "bso2", "bso3"} {
		bso, _ := context.Dispatch.GetBSO(uid, cId, bId)
		assert.Equal("initial payload", bso.Payload)
		assert.Equal(1, bso.SortIndex)
	}

	// Test that updates work
	body = bytes.NewBufferString(`[
		{"id":"bso1", "sortindex": 2},
		{"id":"bso2", "payload": "updated payload"},
		{"id":"bso3", "payload": "updated payload", "sortindex":3}
	]`)

	req2, _ := http.NewRequest("POST", "http://test/1.5/"+uid+"/storage/bookmarks", body)
	req2.Header.Add("Content-Type", "application/json")
	resp = sendrequest(req2, context)
	assert.Equal(http.StatusOK, resp.Code)

	bso, _ := context.Dispatch.GetBSO(uid, cId, "bso1")
	assert.Equal(bso.Payload, "initial payload") // stayed the same
	assert.Equal(bso.SortIndex, 2)               // it updated

	bso, _ = context.Dispatch.GetBSO(uid, cId, "bso2")
	assert.Equal(bso.Payload, "updated payload") // updated
	assert.Equal(bso.SortIndex, 1)               // same

	bso, _ = context.Dispatch.GetBSO(uid, cId, "bso3")
	assert.Equal(bso.Payload, "updated payload") // updated
	assert.Equal(bso.SortIndex, 3)               // updated
}

func TestContextCollectionPOSTNewLines(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	context := makeTestContext()

	uid := "123456"

	// Make sure INSERT works first, with lots of random whitespace
	body := bytes.NewBufferString(`

	{"id":"bso1", "payload": "initial payload", "sortindex": 1, "ttl": 2100000}
{"id":"bso2", "payload": "initial payload", "sortindex": 1, "ttl": 2100000}
   {"id":"bso3", "payload": "initial payload", "sortindex": 1, "ttl": 2100000}


	`)

	req, _ := http.NewRequest("POST", "/1.5/"+uid+"/storage/bookmarks", body)
	req.Header.Add("Content-Type", "application/newlines")

	resp := sendrequest(req, context)
	if !assert.Equal(http.StatusOK, resp.Code) {
		return
	}

	var results PostResults
	jsbody := resp.Body.Bytes()
	err := json.Unmarshal(jsbody, &results)
	if !assert.NoError(err) {
		return
	}

	assert.Len(results.Success, 3)
	assert.Len(results.Failed, 0)

	cId, _ := context.Dispatch.GetCollectionId(uid, "bookmarks")
	for _, bId := range []string{"bso1", "bso2", "bso3"} {
		bso, _ := context.Dispatch.GetBSO(uid, cId, bId)
		assert.Equal("initial payload", bso.Payload)
		assert.Equal(1, bso.SortIndex)
	}

	// Test that updates work
	body = bytes.NewBufferString(`{"id":"bso1", "sortindex": 2}
{"id":"bso2", "payload": "updated payload"}
{"id":"bso3", "payload": "updated payload", "sortindex":3}
	`)

	req2, _ := http.NewRequest("POST", "http://test/1.5/"+uid+"/storage/bookmarks", body)
	req2.Header.Add("Content-Type", "application/newlines")
	resp = sendrequest(req2, context)
	if !assert.Equal(http.StatusOK, resp.Code) {
		return
	}

	bso, _ := context.Dispatch.GetBSO(uid, cId, "bso1")
	assert.Equal(bso.Payload, "initial payload") // stayed the same
	assert.Equal(bso.SortIndex, 2)               // it updated

	bso, _ = context.Dispatch.GetBSO(uid, cId, "bso2")
	assert.Equal(bso.Payload, "updated payload") // updated
	assert.Equal(bso.SortIndex, 1)               // same

	bso, _ = context.Dispatch.GetBSO(uid, cId, "bso3")
	assert.Equal(bso.Payload, "updated payload") // updated
	assert.Equal(bso.SortIndex, 3)               // updated
}

func TestContextCollectionPOSTCreatesCollection(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	context := makeTestContext()

	uid := "123456"

	// Make sure INSERT works first
	body := bytes.NewBufferString(`[
		{"id":"bso1", "payload": "initial payload", "sortindex": 1, "ttl": 2100000},
		{"id":"bso2", "payload": "initial payload", "sortindex": 1, "ttl": 2100000},
		{"id":"bso3", "payload": "initial payload", "sortindex": 1, "ttl": 2100000}
	]`)

	cName := "my_new_collection"

	req, _ := http.NewRequest("POST", "http://test/1.5/"+uid+"/storage/"+cName, body)
	req.Header.Add("Content-Type", "application/json")
	resp := sendrequest(req, context)
	if !assert.Equal(http.StatusOK, resp.Code) {
		return
	}

	cId, err := context.Dispatch.GetCollectionId(uid, cName)
	if !assert.NoError(err) {
		return
	}

	for _, bId := range []string{"bso1", "bso2", "bso3"} {
		b, err := context.Dispatch.GetBSO(uid, cId, bId)
		assert.NotNil(b)
		assert.NoError(err)
	}
}

func TestContextCollectionPOSTWeaveInvalidWSOError(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	context := makeTestContext()

	uid := "123456"

	send := func(body string) *httptest.ResponseRecorder {
		buf := bytes.NewBufferString(body)
		req, _ := http.NewRequest("POST", "http://test/1.5/"+uid+"/storage/col2", buf)
		req.Header.Add("Content-Type", "application/json")
		return sendrequest(req, context)
	}

	{
		resp := send(`[
			{"id":"bso1", "payload": "initial payload", "sortindex": 1, "ttl": 2100000},
			"BOOM"
		]`)
		assert.Equal(WEAVE_INVALID_WBO, resp.Body.String())
	}

	{
		resp := send("42")
		assert.Equal(WEAVE_INVALID_WBO, resp.Body.String())
	}
}

func TestContextCollectionPOSTTooLargePayload(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	context := makeTestContext()

	uid := "123456"
	template := `[{"id":"%s", "payload": "%s", "sortindex": 1, "ttl": 2100000}]`
	bodydata := fmt.Sprintf(template, "test", strings.Repeat("x", syncstorage.MAX_BSO_PAYLOAD_SIZE+1))

	body := bytes.NewBufferString(bodydata)
	req, _ := http.NewRequest("POST", "http://test/1.5/"+uid+"/storage/bookmarks", body)
	req.Header.Add("Content-Type", "application/json")

	res := sendrequest(req, context)
	assert.Equal(http.StatusOK, res.Code)

	var results PostResults
	err := json.Unmarshal(res.Body.Bytes(), &results)
	if !assert.NoError(err) {
		return
	}

	assert.Equal(0, len(results.Success))
	assert.Equal(1, len(results.Failed["test"]))
}

func TestContextCollectionDELETE(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	context := makeTestContext()

	uid := "123456"

	var respData map[string]int

	// delete entire collection
	{
		body := bytes.NewBufferString(`[
			{"id":"bso1", "payload": "initial payload", "sortindex": 1, "ttl": 2100000},
			{"id":"bso2", "payload": "initial payload", "sortindex": 1, "ttl": 2100000},
			{"id":"bso3", "payload": "initial payload", "sortindex": 1, "ttl": 2100000}
		]`)

		req, _ := http.NewRequest("POST", "http://test/1.5/"+uid+"/storage/my_collection", body)
		req.Header.Add("Content-Type", "application/json")
		resp := sendrequest(req, context)
		if !assert.Equal(http.StatusOK, resp.Code) {
			return
		}

		cId, err := context.Dispatch.GetCollectionId(uid, "my_collection")
		if !assert.NoError(err) {
			return
		}

		resp = request("DELETE", "http://test/1.5/"+uid+"/storage/my_collection", nil, context)
		assert.Equal(http.StatusOK, resp.Code)
		err = json.Unmarshal(resp.Body.Bytes(), &respData)
		if !assert.NoError(err) {
			return
		}

		_, err = context.Dispatch.GetCollectionId(uid, "my_collection")
		assert.Exactly(syncstorage.ErrNotFound, err)

		for _, bId := range []string{"bso1", "bso2", "bso3"} {
			b, err := context.Dispatch.GetBSO(uid, cId, bId)
			assert.Nil(b)
			assert.Exactly(syncstorage.ErrNotFound, err)
		}
	}

	// delete only specific ids
	{
		body := bytes.NewBufferString(`[
			{"id":"bso1", "payload": "initial payload", "sortindex": 1, "ttl": 2100000},
			{"id":"bso2", "payload": "initial payload", "sortindex": 1, "ttl": 2100000},
			{"id":"bso3", "payload": "initial payload", "sortindex": 1, "ttl": 2100000}
		]`)

		req, _ := http.NewRequest("POST", "http://test/1.5/"+uid+"/storage/my_collection", body)
		req.Header.Add("Content-Type", "application/json")
		resp := sendrequest(req, context)
		if !assert.Equal(http.StatusOK, resp.Code) {
			fmt.Println(resp.Body.String())
			return
		}

		cId, err := context.Dispatch.GetCollectionId(uid, "my_collection")
		if !assert.NoError(err) {
			return
		}

		resp = request("DELETE", "http://test/1.5/"+uid+"/storage/my_collection?ids=bso1,bso3", nil, context)
		if !assert.Equal(http.StatusOK, resp.Code) {
			return
		}

		err = json.Unmarshal(resp.Body.Bytes(), &respData)
		if !assert.NoError(err) {
			return
		}

		for _, bId := range []string{"bso1", "bso3"} {
			b, err := context.Dispatch.GetBSO(uid, cId, bId)
			assert.Nil(b)
			assert.Exactly(syncstorage.ErrNotFound, err)
		}

		// make sure bso2 is still there
		{
			b, err := context.Dispatch.GetBSO(uid, cId, "bso2")
			assert.NotNil(b)
			assert.NoError(err)
		}
	}
}

func TestContextBsoGET(t *testing.T) {

	t.Parallel()
	assert := assert.New(t)
	context := makeTestContext()
	uid := "123456"
	collection := "bookmarks"
	bsoId := "test"

	var (
		cId int
		err error
	)

	if cId, err = context.Dispatch.GetCollectionId(uid, collection); !assert.NoError(err) {
		return
	}

	payload := syncstorage.String("test")
	sortIndex := syncstorage.Int(100)
	if _, err = context.Dispatch.PutBSO(uid, cId, bsoId, payload, sortIndex, nil); !assert.NoError(err) {
		return
	}

	resp := request("GET", "http://test/1.5/"+uid+"/storage/"+collection+"/"+bsoId, nil, context)
	if !assert.Equal(http.StatusOK, resp.Code) {
		return
	}

	var bso jsonBSO
	if err = json.Unmarshal(resp.Body.Bytes(), &bso); assert.NoError(err) {
		assert.Equal(bsoId, bso.Id)
		assert.Equal(*payload, bso.Payload)
	}

	// test that X-If-Modified-Since and X-If-Unmodified-Since return a 400
	{

		header := make(http.Header)
		header.Add("X-If-Modified-Since", fmt.Sprintf("%.2f", bso.Modified))
		header.Add("X-If-Unmodified-Since", fmt.Sprintf("%.2f", bso.Modified))
		header.Add("Accept", "application/json")
		resp := requestheaders(
			"GET",
			"http://test/1.5/"+uid+"/storage/"+collection+"/"+bsoId,
			nil,
			header,
			context)

		assert.Equal(http.StatusBadRequest, resp.Code)
	}

	// test that api returns a 304 when sending a X-If-Modified-Since header
	{

		header := make(http.Header)
		header.Add("X-If-Modified-Since", fmt.Sprintf("%.2f", bso.Modified))
		resp := requestheaders(
			"GET",
			"http://test/1.5/"+uid+"/storage/"+collection+"/"+bsoId,
			nil,
			header,
			context)

		assert.Equal(http.StatusNotModified, resp.Code)
	}

	// test that api returns a 412 Precondition failed
	{

		header := make(http.Header)
		header.Add("X-If-Unmodified-Since", fmt.Sprintf("%.2f", bso.Modified-0.1))
		resp := requestheaders(
			"GET",
			"http://test/1.5/"+uid+"/storage/"+collection+"/"+bsoId,
			nil,
			header,
			context)
		assert.Equal(http.StatusPreconditionFailed, resp.Code)
	}

	// test that we get a 404 from a bso that doesn't exist
	{
		resp := request("GET", "http://test/1.5/"+uid+"/storage/"+collection+"/nope", nil, context)
		assert.Equal(http.StatusNotFound, resp.Code)
	}

	// test that we get a 404 from a collection that doesn't exist
	{
		resp := request("GET", "http://test/1.5/"+uid+"/storage/nope/nope", nil, context)
		assert.Equal(http.StatusNotFound, resp.Code)
	}
}

func TestContextBsoPUT(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	context := makeTestContext()
	uid := "123456"
	collection := "bookmarks"
	testNum := 0

	cId, _ := context.Dispatch.GetCollectionId(uid, collection)
	{
		testNum++
		header := make(http.Header)
		header.Set("Content-Type", "application/octet-stream")
		resp := requestheaders("PUT", "http://test/1.5/"+uid+"/storage/col/12", nil, header, context)
		if !assert.Equal(http.StatusUnsupportedMediaType, resp.Code) {
			return
		}
	}

	{
		testNum++
		bsoId := "test" + strconv.Itoa(testNum)
		data := `{"id":"` + bsoId + `", "payload":"hello","sortindex":1, "ttl": 1000000}`
		body := bytes.NewBufferString(data)
		resp := jsonrequest("PUT", "http://test/1.5/"+uid+"/storage/"+collection+"/"+bsoId, body, context)
		if !assert.Equal(http.StatusOK, resp.Code) {
			return
		}

		b, err := context.Dispatch.GetBSO(uid, cId, bsoId)
		assert.NotNil(b)
		assert.Equal("hello", b.Payload)
		assert.Equal(1, b.SortIndex)
		assert.NoError(err)
		assert.NotEqual("", resp.Header().Get("X-Last-Modified"))
		assert.NotEqual("", resp.Header().Get("X-Weave-Timestamp"))
	}

	{ // test with fewer params
		testNum++
		bsoId := "test" + strconv.Itoa(testNum)
		data := `{"id":"` + bsoId + `", "payload":"hello","sortindex":1}`
		body := bytes.NewBufferString(data)
		resp := jsonrequest("PUT", "http://test/1.5/"+uid+"/storage/"+collection+"/"+bsoId, body, context)
		if !assert.Equal(http.StatusOK, resp.Code) {
			return
		}

		b, err := context.Dispatch.GetBSO(uid, cId, bsoId)
		assert.NotNil(b)
		assert.NoError(err)
	}

	{ // test with fewer params
		testNum++
		bsoId := "test" + strconv.Itoa(testNum)
		data := `{"id":"` + bsoId + `", "payload":"hello", "sortindex":1}`
		body := bytes.NewBufferString(data)
		resp := jsonrequest("PUT", "http://test/1.5/"+uid+"/storage/"+collection+"/"+bsoId, body, context)
		if !assert.Equal(http.StatusOK, resp.Code) {
			return
		}

		b, err := context.Dispatch.GetBSO(uid, cId, bsoId)
		assert.NotNil(b)
		assert.NoError(err)
	}

	{ // Test Updates
		testNum++
		bsoId := "test" + strconv.Itoa(testNum)
		data := `{"id":"` + bsoId + `", "payload":"hello", "sortindex":1}`
		body := bytes.NewBufferString(data)
		resp := jsonrequest("PUT", "http://test/1.5/"+uid+"/storage/"+collection+"/"+bsoId, body, context)
		if !assert.Equal(http.StatusOK, resp.Code) {
			return
		}

		b, err := context.Dispatch.GetBSO(uid, cId, bsoId)
		assert.NotNil(b)
		assert.NoError(err)

		data = `{"id":"` + bsoId + `", "payload":"updated", "sortindex":2}`
		body = bytes.NewBufferString(data)
		resp = jsonrequest("PUT", "http://test/1.5/"+uid+"/storage/"+collection+"/"+bsoId, body, context)
		if !assert.Equal(http.StatusOK, resp.Code) {
			return
		}

		b, err = context.Dispatch.GetBSO(uid, cId, bsoId)
		assert.NotNil(b)
		assert.NoError(err)
		assert.Equal("updated", b.Payload)
		assert.Equal(2, b.SortIndex)
	}
}

func TestContextBsoPUTWeaveInvalidWSOError(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	context := makeTestContext()
	uid := "123456"
	collection := "bookmarks"
	bId := "test"

	{
		data := `{"id": [1,2,3], "payload":"hello", "sortindex":1}`
		body := bytes.NewBufferString(data)
		resp := jsonrequest("PUT", "http://test/1.5/"+uid+"/storage/"+collection+"/"+bId, body, context)
		if assert.Equal(http.StatusBadRequest, resp.Code) {
			assert.Equal(WEAVE_INVALID_WBO, resp.Body.String())
		}
	}

	{
		data := "42"
		body := bytes.NewBufferString(data)
		resp := jsonrequest("PUT", "http://test/1.5/"+uid+"/storage/"+collection+"/"+bId, body, context)
		if assert.Equal(http.StatusBadRequest, resp.Code) {
			assert.Equal(WEAVE_INVALID_WBO, resp.Body.String())
		}
	}
}

func TestContextBsoDELETE(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	context := makeTestContext()
	uid := "123456"
	collection := "bookmarks"
	bId := "test"

	cId, _ := context.Dispatch.GetCollectionId(uid, collection)

	resp404 := request("DELETE", "/1.5/"+uid+"/storage/"+collection+"/"+"NOT_EXISTS", nil, context)
	if !assert.Equal(http.StatusNotFound, resp404.Code) {
		return
	}

	if _, err := context.Dispatch.PutBSO(uid, cId, bId, syncstorage.String("hi"), nil, nil); !assert.NoError(err) {
		return
	}

	resp := request("DELETE", "/1.5/"+uid+"/storage/"+collection+"/"+bId, nil, context)
	if !assert.Equal(http.StatusOK, resp.Code) ||
		!assert.NotEqual("", resp.Header().Get("X-Last-Modified")) {
		return
	}

	b, err := context.Dispatch.GetBSO(uid, cId, bId)
	assert.Exactly(syncstorage.ErrNotFound, err)
	assert.Nil(b)
}

func TestContextDelete(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	context := makeTestContext()
	uid := "123456"

	var (
		cId int
		err error
	)

	for _, url := range []string{"/1.5/" + uid, "/1.5/" + uid + "/storage"} {
		if cId, err = context.Dispatch.CreateCollection(uid, "my_collection"); !assert.NoError(err) {
			return
		}

		bId := "test"
		payload := "data"
		if _, err = context.Dispatch.PutBSO(uid, cId, bId, &payload, nil, nil); !assert.NoError(err) {
			return
		}

		resp := request("DELETE", url, nil, context)
		if !assert.Equal(http.StatusOK, resp.Code, url) ||
			!assert.NotEqual("", resp.Header().Get("X-Last-Modified"), url) {
			return
		}

		b, err := context.Dispatch.GetBSO(uid, cId, bId)
		assert.Exactly(syncstorage.ErrNotFound, err)
		assert.Nil(b)

		cTest, err := context.Dispatch.GetCollectionId(uid, "my_collection")
		assert.Exactly(syncstorage.ErrNotFound, err)
		assert.Equal(0, cTest)
	}

}

// Some DB functions use sql.Row.Scan() into a real type
// and that caused sql errors, need to use the NullString, NullX
// types to make sure the db returned something before actually
// doing a type conversion
func TestContextDeleteAndDBScanBug(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	context := makeTestContext()
	uid := "144819"
	resp := request("DELETE", "/1.5/"+uid, nil, context)
	assert.Equal(http.StatusOK, resp.Code)

	_, err := context.Dispatch.LastModified(uid)
	assert.NoError(err)

	_, err = context.Dispatch.GetCollectionModified(uid, 1)
	assert.NoError(err)

	_, err = context.Dispatch.GetCollectionId(uid, "bookmarks")
	assert.Equal(syncstorage.ErrNotFound, err)
}