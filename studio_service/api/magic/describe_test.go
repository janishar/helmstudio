package magic

import "testing"

// TestDescribe: an edit in the board's words (API 1.87, the Assistant's summary).
func TestDescribe(t *testing.T) {
	block := func(id, lane string, at int, kind, name string) *Block {
		return &Block{ID: id, Lane: lane, At: at, Length: 24, Kind: kind, Name: name, Request: map[string]any{}, Takes: []string{}, Effects: []Effect{}}
	}
	a := Empty(DefaultTarget)
	a.Lanes = []*Lane{{ID: "s", Name: "Shots", Role: "shots"}, {ID: "v", Name: "Voice", Role: "voice"}}
	a.Blocks = []*Block{block("s1", "s", 0, "video", "Shot 1 · rain"), block("l2", "v", 0, "voice", "Line 2"),
		block("s5", "s", 24, "video", "Shot 5")}
	b := a.Clone()
	b.Blocks = []*Block{block("s1", "s", 0, "video", "Shot 1 · rain"), block("l2", "v", 48, "voice", "Line 2"),
		block("s2", "s", 24, "video", "Shot 2"), block("f4", "s", 48, "frame", "Frame 4")}
	b.Blocks[0].Name = "Shot 1 · wet"
	if got := Describe(a, b); got != "Added Shot 2 and Frame 4; moved Line 2; removed Shot 5; changed Shot 1" {
		t.Fatal(got)
	}
	c := b.Clone()
	for i, id := range []string{"x1", "x2", "x3"} {
		c.Blocks = append(c.Blocks, block(id, "s", 72+i*24, "video", "Shot x"))
	}
	if got := Describe(b, c); got != "Added 3 shots" {
		t.Fatal(got)
	}
	c.Blocks = append(c.Blocks, block("f9", "s", 200, "frame", "Frame 9"))
	if got := Describe(b, c); got != "Added 3 shots and Frame 9" {
		t.Fatal(got)
	}
	d := a.Clone()
	d.Lanes = append(d.Lanes, &Lane{ID: "m", Name: "Music", Role: "music"})
	if got := Describe(a, d); got != "Added lane Music" {
		t.Fatal(got)
	}
	if got := Describe(a, a); got != "No change" {
		t.Fatal(got)
	}
}
