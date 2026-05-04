#!/usr/bin/env python3
"""IU voice TTS via local vllm-mlx OpenAI-compatible /v1/audio/speech.

Uses chatterbox-multilingual + voice iu + lang_code ko.
Respects LOCAL_LLM_BASE_URL (default http://localhost:8000).

Example:
  ./scripts/tts_iu.py "안녕하세요." -o out.wav
  ./scripts/tts_iu.py -f speech.txt -o out.wav
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.error
import urllib.request

DEFAULT_BASE = os.environ.get("LOCAL_LLM_BASE_URL", "http://localhost:8000").rstrip("/")
MAX_INPUT_CHARS = 4096  # server default in many builds; see docs/VLLM_API.md


def main() -> int:
    p = argparse.ArgumentParser(description="TTS with IU voice (Korean, chatterbox-multilingual).")
    g = p.add_mutually_exclusive_group(required=True)
    g.add_argument("text", nargs="?", help="Text to synthesize")
    g.add_argument("-f", "--file", metavar="PATH", help="Read text from UTF-8 file")
    p.add_argument(
        "-o",
        "--output",
        default="iu_tts.wav",
        metavar="PATH",
        help="Output WAV path (default: iu_tts.wav)",
    )
    p.add_argument(
        "--base-url",
        default=DEFAULT_BASE,
        help=f"API base URL (default: env LOCAL_LLM_BASE_URL or {DEFAULT_BASE!r})",
    )
    args = p.parse_args()

    if args.file:
        with open(args.file, encoding="utf-8") as fp:
            text = fp.read()
        if text.startswith("\ufeff"):
            text = text[1:]
    else:
        text = args.text or ""

    text = text.strip()
    if not text:
        print("error: empty input", file=sys.stderr)
        return 1
    if len(text) > MAX_INPUT_CHARS:
        print(
            f"error: input is {len(text)} characters (max {MAX_INPUT_CHARS}); split or shorten",
            file=sys.stderr,
        )
        return 1

    body = json.dumps(
        {
            "model": "chatterbox-multilingual",
            "input": text,
            "voice": "iu",
            "response_format": "wav",
            "lang_code": "ko",
        },
        ensure_ascii=False,
    ).encode("utf-8")

    url = f"{args.base_url.rstrip('/')}/v1/audio/speech"
    req = urllib.request.Request(
        url,
        data=body,
        method="POST",
        headers={"Content-Type": "application/json; charset=utf-8"},
    )

    try:
        with urllib.request.urlopen(req, timeout=600) as resp:
            raw = resp.read()
    except urllib.error.HTTPError as e:
        err = e.read().decode("utf-8", errors="replace")
        print(f"HTTP {e.code}: {err}", file=sys.stderr)
        return 1
    except urllib.error.URLError as e:
        print(f"request failed: {e}", file=sys.stderr)
        return 1

    if raw[:4] != b"RIFF":
        print("error: response is not a WAV file (check server logs)", file=sys.stderr)
        return 1

    out = args.output
    with open(out, "wb") as fp:
        fp.write(raw)
    print(f"wrote {len(raw)} bytes to {out}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
