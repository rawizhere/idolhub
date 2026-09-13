package xscraper

import (
	"fmt"
	"testing"
)

func TestParseQueryIDs(t *testing.T) {
	bundle := []byte(`e.exports={queryId:"eoJ5zbv51Z_KVl81v9PmLQ",operationName:"UserTweets",metadata:{featureSwitches:[]}},
	t.exports={operationName:"UserByScreenName",queryId:"Gb-d6r0vxPOADdG62OEBpQ",operationType:"query"},
	ignored={queryId:"zzz",operationName:"TweetDetail"}`)
	ops := parseQueryIDs(bundle)
	if got := ops["UserTweets"]; got != "eoJ5zbv51Z_KVl81v9PmLQ" {
		t.Fatalf("UserTweets: got %q", got)
	}
	if got := ops["UserByScreenName"]; got != "Gb-d6r0vxPOADdG62OEBpQ" {
		t.Fatalf("UserByScreenName: got %q", got)
	}
	if _, ok := ops["TweetDetail"]; ok {
		t.Fatal("parsed an operation outside the known set")
	}
}

func TestIsUnknownQueryErr(t *testing.T) {
	if !isUnknownQueryErr(fmt.Errorf(`{"errors":[{"message":"Cannot find query with id: abc"}]}`)) {
		t.Fatal("unknown-query error not recognized")
	}
	if isUnknownQueryErr(nil) {
		t.Fatal("nil error recognized")
	}
}
