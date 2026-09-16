package timeline

import (
	"fmt"
	"math"
)

// Rate is a frame rate as the exact ratio it is. 23.976 is 24000/1001, which
// no JSON number writes, so the document carries the familiar decimal and the
// arithmetic uses the ratio (M8 Q7).
type Rate struct{ Num, Den int64 }

// rates is every frame rate a target may declare.
var rates = map[float64]Rate{
	23.976: {24000, 1001},
	24:     {24, 1},
	25:     {25, 1},
	29.97:  {30000, 1001},
	30:     {30, 1},
	48:     {48, 1},
	50:     {50, 1},
	59.94:  {60000, 1001},
	60:     {60, 1},
}

// RateOf is the ratio behind a target's fps. A decimal that is not one of the
// rates is refused rather than guessed at.
func RateOf(fps float64) (Rate, bool) {
	for decimal, r := range rates {
		if math.Abs(decimal-fps) < 1e-6 {
			return r, true
		}
	}
	return Rate{}, false
}

// Frames is the whole frame a time falls on, rounded to the nearest.
func (r Rate) Frames(seconds float64) int64 {
	return int64(math.Round(seconds * float64(r.Num) / float64(r.Den)))
}

// EvenFrames rounds to the nearest even number of frames, so that half of it
// is a whole frame — which a dissolve centred on a cut needs.
func (r Rate) EvenFrames(seconds float64) int64 {
	return int64(math.Round(seconds*float64(r.Num)/float64(r.Den)/2)) * 2
}

// Seconds is where a whole frame falls, exactly.
func (r Rate) Seconds(frames int64) float64 {
	return float64(frames) * float64(r.Den) / float64(r.Num)
}

// String is the ratio as ffmpeg writes it.
func (r Rate) String() string { return fmt.Sprintf("%d/%d", r.Num, r.Den) }

// Equal reports whether another ratio is the same rate, in lowest terms.
func (r Rate) Equal(o Rate) bool {
	if r.Num == 0 || r.Den == 0 || o.Num == 0 || o.Den == 0 {
		return false
	}
	return r.Num*o.Den == o.Num*r.Den
}
