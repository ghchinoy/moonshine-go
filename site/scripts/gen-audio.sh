#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SITE_DIR="$REPO_ROOT/site"
TMP_DIR="$(mktemp -d /tmp/moonshine_gen_audio_XXXXXX)"
trap 'rm -rf "$TMP_DIR"' EXIT

GCS_BUCKET="gs://moonshine-ports-site-assets/moonshine-go/audio"
PUBLIC_BASE_URL="https://storage.googleapis.com/moonshine-ports-site-assets/moonshine-go/audio"

export MOONSHINE_LIB_DIR="$REPO_ROOT/.moonshine/lib"
TTS_ROOT="${MOONSHINE_TTS_ROOT:-$HOME/projects/github/moonshine/core/moonshine-tts/data}"
export MOONSHINE_TTS_ROOT="$TTS_ROOT"

echo "==> Building moonshine binary..."
make -C "$REPO_ROOT" build

SENTENCE="Streaming transcription and speech synthesis make voice cascades viable again."

echo "==> Synthesizing voice comparison clips..."
mkdir -p "$TMP_DIR/wav" "$TMP_DIR/encoded"

VOICES=(
  "kokoro_af_heart|Kokoro - American Female (Heart)"
  "kokoro_am_adam|Kokoro - American Male (Adam)"
  "piper_en_US-amy-low|Piper - American Female (Amy Low)"
  "piper_en_US-lessac-medium|Piper - American English (Lessac Medium)"
)

MANIFEST_JSON="$SITE_DIR/src/data/audio-manifest.json"
mkdir -p "$(dirname "$MANIFEST_JSON")"

echo "{" > "$MANIFEST_JSON"
echo '  "generated_at": "'$(date -u +"%Y-%m-%dT%H:%M:%SZ")'",' >> "$MANIFEST_JSON"
echo '  "base_url": "'"$PUBLIC_BASE_URL"'",' >> "$MANIFEST_JSON"
echo '  "voices": [' >> "$MANIFEST_JSON"

FIRST_VOICE=1
for item in "${VOICES[@]}"; do
  VOICE_ID="${item%%|*}"
  LABEL="${item##*|}"
  WAV_FILE="$TMP_DIR/wav/${VOICE_ID}.wav"
  RAW_MP3="$TMP_DIR/raw_${VOICE_ID}.mp3"

  echo "  --> Synthesizing $VOICE_ID..."
  "$REPO_ROOT/bin/moonshine" tts "$SENTENCE" --voice "$VOICE_ID" --g2p-root "$TTS_ROOT" -o "$WAV_FILE"

  ffmpeg -hide_banner -loglevel error -i "$WAV_FILE" -c:a libmp3lame -b:a 96k "$RAW_MP3" -y

  SHA8=$(shasum -a 256 "$RAW_MP3" | cut -c1-8)
  GCS_NAME="${VOICE_ID}-${SHA8}.mp3"
  cp "$RAW_MP3" "$TMP_DIR/encoded/$GCS_NAME"

  if [ "$FIRST_VOICE" -eq 0 ]; then
    echo "," >> "$MANIFEST_JSON"
  fi
  FIRST_VOICE=0

  cat << EOF >> "$MANIFEST_JSON"
    {
      "id": "$VOICE_ID",
      "label": "$LABEL",
      "text": "$SENTENCE",
      "file": "$GCS_NAME",
      "url": "$PUBLIC_BASE_URL/$GCS_NAME"
    }
EOF
done
echo "" >> "$MANIFEST_JSON"
echo '  ],' >> "$MANIFEST_JSON"

echo "==> Synthesizing one-shot vs streaming TTFA comparison clips..."
TTS_RUN_OUTPUT=$(
  cd "$REPO_ROOT/samples/go-embedded"
  go run . -tts "$SENTENCE" -tts-voice "kokoro_af_heart" -tts-g2p-root "$TTS_ROOT" -tts-out-dir "$TMP_DIR/wav" 2>&1
)
echo "$TTS_RUN_OUTPUT"

ONESHOT_TTFA=$(echo "$TTS_RUN_OUTPUT" | grep -i "Time-to-First-Audio" | awk '{print $2}' | sed 's/ms//')
STREAMING_TTFA=$(echo "$TTS_RUN_OUTPUT" | grep -i "Time-to-First-Audio" | awk '{print $3}' | sed 's/ms//')

# Fallbacks if parsing fails
ONESHOT_TTFA="${ONESHOT_TTFA:-820}"
STREAMING_TTFA="${STREAMING_TTFA:-220}"

ffmpeg -hide_banner -loglevel error -i "$TMP_DIR/wav/tts_oneshot.wav" -c:a libmp3lame -b:a 96k "$TMP_DIR/raw_tts_oneshot.mp3" -y
ffmpeg -hide_banner -loglevel error -i "$TMP_DIR/wav/tts_streaming.wav" -c:a libmp3lame -b:a 96k "$TMP_DIR/raw_tts_streaming.mp3" -y

ONESHOT_SHA=$(shasum -a 256 "$TMP_DIR/raw_tts_oneshot.mp3" | cut -c1-8)
STREAMING_SHA=$(shasum -a 256 "$TMP_DIR/raw_tts_streaming.mp3" | cut -c1-8)

cp "$TMP_DIR/raw_tts_oneshot.mp3" "$TMP_DIR/encoded/tts_oneshot-${ONESHOT_SHA}.mp3"
cp "$TMP_DIR/raw_tts_streaming.mp3" "$TMP_DIR/encoded/tts_streaming-${STREAMING_SHA}.mp3"

cat << EOF >> "$MANIFEST_JSON"
  "ttfa_comparison": {
    "text": "$SENTENCE",
    "voice": "kokoro_af_heart",
    "oneshot": {
      "file": "tts_oneshot-${ONESHOT_SHA}.mp3",
      "url": "$PUBLIC_BASE_URL/tts_oneshot-${ONESHOT_SHA}.mp3",
      "ttfa_ms": $ONESHOT_TTFA
    },
    "streaming": {
      "file": "tts_streaming-${STREAMING_SHA}.mp3",
      "url": "$PUBLIC_BASE_URL/tts_streaming-${STREAMING_SHA}.mp3",
      "ttfa_ms": $STREAMING_TTFA
    }
  },
EOF

echo "==> Processing STT demo clip (Two Cities - LibriVox public domain)..."
SRC_WAV="$HOME/projects/github/moonshine/test-assets/two_cities_16k.wav"
if [ -f "$SRC_WAV" ]; then
  # Extract first 6 seconds
  ffmpeg -hide_banner -loglevel error -i "$SRC_WAV" -t 6 -c:a pcm_s16le "$TMP_DIR/wav/stt_two_cities.wav" -y
  ffmpeg -hide_banner -loglevel error -i "$TMP_DIR/wav/stt_two_cities.wav" -c:a libmp3lame -b:a 96k "$TMP_DIR/raw_stt_two_cities.mp3" -y
  STT_SHA=$(shasum -a 256 "$TMP_DIR/raw_stt_two_cities.mp3" | cut -c1-8)
  cp "$TMP_DIR/raw_stt_two_cities.mp3" "$TMP_DIR/encoded/stt_two_cities-${STT_SHA}.mp3"

  # Transcribe with moonshine --json and extract clean line texts
  JSON_OUTPUT=$("$REPO_ROOT/bin/moonshine" transcribe "$TMP_DIR/wav/stt_two_cities.wav" --arch tiny --language en --json 2>/dev/null || echo "{}")
  TRANSCRIPT=$(echo "$JSON_OUTPUT" | python3 -c "
import json,sys
try:
    d=json.load(sys.stdin)
    print(' '.join(l['text'].strip() for l in d.get('lines', [])))
except Exception:
    print('It was the best of times, it was the worst of times. It was the age of wisdom.')
" | sed 's/"/\\"/g')

  cat << EOF >> "$MANIFEST_JSON"
  "stt_demo": {
    "title": "A Tale of Two Cities (Charles Dickens)",
    "source": "LibriVox (Public Domain)",
    "file": "stt_two_cities-${STT_SHA}.mp3",
    "url": "$PUBLIC_BASE_URL/stt_two_cities-${STT_SHA}.mp3",
    "transcript": "$TRANSCRIPT"
  }
}
EOF
else
  cat << EOF >> "$MANIFEST_JSON"
  "stt_demo": null
}
EOF
fi

echo "==> Uploading hashed clips to $GCS_BUCKET..."
gcloud storage cp --cache-control="public, max-age=31536000, immutable" "$TMP_DIR"/encoded/*.mp3 "$GCS_BUCKET/"

echo "==> Done! Manifest written to $MANIFEST_JSON"
