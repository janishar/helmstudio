#!/bin/sh
# Makes the media the export tests run against, and is committed beside what it
# made, so the fixtures are bytes in the repository rather than something each
# machine encodes for itself. Run it by hand, look at what changed, and commit
# the files with it.
#
#     sh test/media/make-fixtures.sh
#
# Every clip is tiny and synthetic: a test pattern for the picture, and for the
# sound a 20 ms tone at the very start followed by silence. That burst is what
# lets a test say where a clip's sound begins in an export, which is how the
# priming a copied AAC stream drags into every cut is caught
# (docs/agents/reports/08-timeline-and-export.md, "What I measured").
set -eu

dir=$(cd "$(dirname "$0")" && pwd)/fixtures
mkdir -p "$dir"
FFMPEG=${HELM_FFMPEG:-ffmpeg}

# The encoders are the ones the licensing decision allows (docs/decisions.md
# M8 Q4): videotoolbox for the picture, ffmpeg's own AAC for the sound.
video_out="-c:v h264_videotoolbox -profile:v high -allow_sw 1 -pix_fmt yuv420p -b:v 1M"
audio_out="-c:a aac -b:a 128k -ar 48000 -ac 2"
mp4_out="-fflags +bitexact -movflags +faststart"
common="-hide_banner -loglevel error -y"

# take-a and take-b are the common case the fast path exists for: two clips
# from one studio, same size, same rate, same settings, so their parameter sets
# match and a copy is legal.
clip() { # name duration size rate tone
	# shellcheck disable=SC2086
	$FFMPEG $common \
		-f lavfi -i "testsrc2=size=$3:rate=$4:duration=$2" \
		-f lavfi -i "sine=f=$5:d=0.02:r=48000,volume=6,apad=whole_dur=$2,aformat=channel_layouts=stereo" \
		$video_out $audio_out $mp4_out -t "$2" "$dir/$1.mp4"
}

clip take-a 2 320x180 24 1000
clip take-b 1.5 320x180 24 1500
clip take-c-25fps 1.5 320x180 25 2000
clip take-d-640x360 1.5 640x360 24 2500

# A clip with no sound at all, so the graph has to invent silence for it.
# shellcheck disable=SC2086
$FFMPEG $common -f lavfi -i "testsrc2=size=320x180:rate=24:duration=1" $video_out $mp4_out -an -t 1 "$dir/take-e-silent.mp4"

# A still, which is drawn rather than copied, and a voice line at another
# sample rate and one channel, which the mix has to resample and spread.
$FFMPEG $common -f lavfi -i "testsrc2=size=320x180:rate=1:duration=1" -frames:v 1 "$dir/still.png"
$FFMPEG $common -f lavfi -i "sine=f=800:d=0.02:r=44100,volume=6,apad=whole_dur=1,aformat=channel_layouts=mono" \
	-c:a pcm_s16le -ar 44100 -ac 1 "$dir/voice.wav"

echo "fixtures written to $dir"
ls -l "$dir"
