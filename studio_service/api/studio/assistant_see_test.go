package studio

import (
	"encoding/json"
	"testing"
)

// An ok reply with no pictures (an audio take, a video with no poster) keeps "images": [] as req.studio.see's ok
// requires; an error reply has no images key (its shape allows none).
func TestSeeReplyJSON(t *testing.T) {
	title, pics := "a song", [][]byte{}
	ok, _ := json.Marshal(SeeReply{TakeID: "t", Kind: "audio", Title: &title, Images: &pics})
	if got := string(ok); got != `{"take_id":"t","kind":"audio","title":"a song","images":[]}` {
		t.Fatalf("ok reply: %s", got)
	}
	bad, _ := json.Marshal(SeeReply{Error: "not found", Code: "not_found"})
	if got := string(bad); got != `{"error":"not found","code":"not_found"}` {
		t.Fatalf("error reply: %s", got)
	}
}
