# vllm-mlx API Spec

> Base URL: `http://localhost:8000`  
> 인증: 기본 비활성화. `--api-key` 옵션으로 활성화 시 `Authorization: Bearer <key>` 또는 `x-api-key: <key>` 헤더 필요.

---

## 목차

- [API와 도구 연결](#api와-도구-연결)
- [시스템](#시스템)
- [모델](#모델)
- [채팅 (OpenAI)](#채팅-openai)
- [채팅 (Anthropic)](#채팅-anthropic)
- [Responses (OpenAI)](#responses-openai)
- [텍스트 완성](#텍스트-완성)
- [STT (음성 인식)](#stt-음성-인식)
- [TTS (음성 합성)](#tts-음성-합성)
- [MCP](#mcp)
- [임베딩](#임베딩)
- [재순위](#재순위)
- [캐시](#캐시)

---

## API와 도구 연결

vllm-mlx **HTTP API**와 **MCP 도구**가 어떻게 연결되는지 정리한다. (여기서 “도구”는 Cursor/Claude 등에 붙는 MCP `tools/list`의 항목과, 채팅 요청의 OpenAI `tools` 배열을 모두 포함한다.)

### mcp-local-llm MCP 서버 → vllm-mlx

에디터·에이전트가 **mcp-local-llm** 바이너리를 MCP 서버로 쓸 때, 각 MCP 도구가 가리키는 vllm-mlx 호출은 아래와 같다.

| MCP 도구 | 호출하는 vllm-mlx API | 비고 |
|----------|----------------------|------|
| `call_local_llm` | `POST /v1/chat/completions` | OpenAI Chat Completions JSON (`model`, `messages`, 선택 `max_tokens`). Base URL은 환경 변수 `LOCAL_LLM_BASE_URL` (미설정 시 `http://localhost:8000`). |
| `get_llm_usage` | — | vllm-mlx를 호출하지 않음. 바이너리와 같은 디렉터리의 `usage.json`만 읽고 갱신. |

`call_local_llm`은 **`/v1/mcp/*` MCP 프록시 API를 거치지 않는다.**

### 채팅 API의 function calling (`tools` 필드)

클라이언트가 `POST /v1/chat/completions` 본문에 `tools` / `tool_choice`를 넣으면, 모델 응답에 `tool_calls` 등이 포함될 수 있다. **도구 정의의 실행(HTTP 콜백 등)은 vllm-mlx가 아니라 요청을 보낸 클라이언트**가 처리하는 일반적인 OpenAI 호환 흐름이다.

| API | 요청에서 도구 관련 필드 | 역할 |
|-----|-------------------------|------|
| `POST /v1/chat/completions` | `tools`, `tool_choice` | 스키마에 맞는 **모델 출력** 유도. 실제 외부 도구 실행은 클라이언트 책임. |

### vllm-mlx MCP 프록시 → 연동 MCP 서버

vllm-mlx에 **별도 MCP 서버**가 연결되어 있을 때, 아래 HTTP로 그 서버들의 도구를 조회·실행한다. (mcp-local-llm과는 독립된 경로.)

| vllm-mlx API | 도구(MCP) 연결 관점에서의 역할 |
|--------------|--------------------------------|
| `GET /v1/mcp/tools` | 연결된 MCP 서버가 노출하는 도구 목록 (`name`, `description`, `server` 등). |
| `GET /v1/mcp/servers` | 연결된 MCP 서버 목록. |
| `POST /v1/mcp/execute` | `server`, `tool`, `arguments`로 **해당 MCP 도구를 HTTP에서 직접 실행**. |

---

## 시스템

### `GET /health`
서버 상태 확인.

**응답:**
```json
{"status": "ok"}
```

### `GET /metrics`
Prometheus 형식 메트릭.

### `GET /v1/status`
모델 로드 상태, 설정 정보.

---

## 모델

### `GET /v1/models`
로드된 모델 목록.

**응답:**
```json
{
  "object": "list",
  "data": [
    {"id": "mlx-community/gemma-4-26b-a4b-it-4bit", "object": "model"}
  ]
}
```

---

## 채팅 (OpenAI)

### `POST /v1/chat/completions`

OpenAI Chat Completions API 호환.

**요청:**
```json
{
  "model": "mlx-community/gemma-4-26b-a4b-it-4bit",
  "messages": [
    {"role": "system", "content": "You are a helpful assistant."},
    {"role": "user", "content": "안녕하세요!"}
  ],
  "max_tokens": 500,
  "temperature": 0.7,
  "stream": false
}
```

**멀티모달 (이미지 포함):**
```json
{
  "model": "mlx-community/gemma-4-26b-a4b-it-4bit",
  "messages": [{
    "role": "user",
    "content": [
      {"type": "text", "text": "이 이미지를 설명해줘"},
      {"type": "image_url", "image_url": {"url": "https://..."}}
    ]
  }],
  "max_tokens": 300
}
```

**Tool Calling:**
```json
{
  "model": "mlx-community/gemma-4-26b-a4b-it-4bit",
  "messages": [{"role": "user", "content": "서울 날씨 알려줘"}],
  "tools": [{
    "type": "function",
    "function": {
      "name": "get_weather",
      "description": "현재 날씨 조회",
      "parameters": {
        "type": "object",
        "properties": {
          "location": {"type": "string", "description": "도시 이름"}
        },
        "required": ["location"]
      }
    }
  }],
  "tool_choice": "auto"
}
```

**응답:**
```json
{
  "id": "chatcmpl-xxxx",
  "object": "chat.completion",
  "model": "mlx-community/gemma-4-26b-a4b-it-4bit",
  "choices": [{
    "index": 0,
    "message": {"role": "assistant", "content": "..."},
    "finish_reason": "stop"
  }],
  "usage": {"prompt_tokens": 10, "completion_tokens": 50, "total_tokens": 60}
}
```

**파라미터:**

| 파라미터 | 타입 | 기본값 | 설명 |
|---------|------|--------|------|
| `model` | string | 필수 | 모델 ID |
| `messages` | array | 필수 | 대화 내역 |
| `max_tokens` | int | 32768 | 최대 출력 토큰 |
| `temperature` | float | 0.7 | 창의성 (0.0~2.0) |
| `top_p` | float | 1.0 | nucleus sampling |
| `stream` | bool | false | 스트리밍 응답 |
| `tools` | array | - | 도구 정의 목록 |
| `tool_choice` | string | "auto" | 도구 선택 방식 |

---

## 채팅 (Anthropic)

### `POST /v1/messages`

Anthropic Messages API 호환. Claude Code, Claude SDK와 직접 연결 가능.

**요청:**
```json
{
  "model": "mlx-community/gemma-4-26b-a4b-it-4bit",
  "messages": [{"role": "user", "content": "안녕하세요!"}],
  "max_tokens": 500
}
```

**헤더:**
```
x-api-key: dummy
anthropic-version: 2023-06-01
```

**응답:**
```json
{
  "id": "msg_xxxx",
  "type": "message",
  "role": "assistant",
  "model": "mlx-community/gemma-4-26b-a4b-it-4bit",
  "content": [{"type": "text", "text": "..."}],
  "stop_reason": "end_turn",
  "usage": {"input_tokens": 10, "output_tokens": 50}
}
```

### `POST /v1/messages/count_tokens`

입력 토큰 수 계산.

**요청:**
```json
{
  "model": "mlx-community/gemma-4-26b-a4b-it-4bit",
  "messages": [{"role": "user", "content": "안녕하세요!"}]
}
```

---

## 텍스트 완성

### `POST /v1/completions`

OpenAI Completions API 호환 (레거시).

**요청:**
```json
{
  "model": "mlx-community/gemma-4-26b-a4b-it-4bit",
  "prompt": "한국의 수도는",
  "max_tokens": 50
}
```

---

## STT (음성 인식)

### `POST /v1/audio/transcriptions`

OpenAI Whisper API 호환. `multipart/form-data` 전송.

**요청:**
```bash
curl http://localhost:8000/v1/audio/transcriptions \
  -F "file=@audio.wav" \
  -F "model=whisper-1" \
  -F "language=ko"
```

**파라미터:**

| 파라미터 | 타입 | 필수 | 설명 |
|---------|------|:----:|------|
| `file` | file | ✅ | 오디오 파일 (wav, mp3, m4a 등) |
| `model` | string | ✅ | `whisper-1` 고정 |
| `language` | string | - | 언어 코드 (`ko`, `en` 등). 생략 시 자동 감지 |

**응답:**
```json
{"text": "안녕하세요.", "language": "ko", "duration": null}
```

**내부 모델:** `mlx-community/whisper-large-v3-mlx`

---

## TTS (음성 합성)

### `POST /v1/audio/speech`

OpenAI TTS API 호환. JSON body 전송.

**요청:**
```json
{
  "model": "chatterbox-multilingual",
  "input": "안녕하세요.",
  "voice": "wc",
  "response_format": "wav",
  "lang_code": "ko"
}
```

**응답:** 바이너리 오디오 파일 (`audio/wav`)

**파라미터:**

| 파라미터 | 타입 | 기본값 | 설명 |
|---------|------|--------|------|
| `model` | string | `kokoro` | TTS 모델 별칭 |
| `input` | string | 필수 | 합성할 텍스트 |
| `voice` | string | `af_heart` | 음성 ID (모델별 상이) |
| `speed` | float | `1.0` | 속도 (0.5~2.0) |
| `response_format` | string | `wav` | `wav` 또는 `mp3` |
| `lang_code` | string | `a` | 언어 코드 |
| `volume` | float | `0.9` | 출력 음량 (0.0~1.0). RMS 정규화 후 적용 |

**지원 모델:**

| `model` 값 | 실제 모델 | 특징 |
|-----------|----------|------|
| `kokoro` | `mlx-community/Kokoro-82M-bf16` | 영어 특화, 빠름 |
| `kokoro-4bit` | `mlx-community/Kokoro-82M-4bit` | 영어, 경량 |
| `chatterbox` | `mlx-community/chatterbox-turbo-fp16` | 영어, 고품질 |
| `chatterbox-multilingual` | `litmudoc/Chatterbox-Multilingual-MLX-v2-fp16` | **다국어(한국어 포함), Voice Cloning** |
| `vibevoice` | `mlx-community/VibeVoice-Realtime-0.5B-4bit` | 실시간 |
| `voxcpm` | `mlx-community/VoxCPM1.5` | 중국어/영어 |

**lang_code 값 (`chatterbox-multilingual`):**

| 코드 | 언어 |
|------|------|
| `a` | 영어 (American) |
| `ko` | 한국어 |
| `ja` | 일본어 |
| `zh` | 중국어 |
| `es` | 스페인어 |
| `fr` | 프랑스어 |

**Kokoro voice 목록:**

| voice | 성별 | 억양 |
|-------|:----:|------|
| `af_heart` | 여 | American |
| `af_bella` | 여 | American |
| `af_nicole` | 여 | American |
| `af_sarah` | 여 | American |
| `af_sky` | 여 | American |
| `am_adam` | 남 | American |
| `am_michael` | 남 | American |
| `bf_emma` | 여 | British |
| `bf_isabella` | 여 | British |
| `bm_george` | 남 | British |
| `bm_lewis` | 남 | British |

**Chatterbox-Multilingual 저장 목소리 (Voice Cloning):**

| `voice` 값 | 설명 |
|-----------|------|
| `wc` | 사용자 본인 목소리 |
| `iu` | IU 목소리 |
| `default` | 모델 기본 목소리 |

> 새 목소리 추가 방법은 README 참고.

### `GET /v1/audio/voices`

사용 가능한 TTS voice 목록.

**응답:**
```json
{"voices": ["af_heart", "af_bella", ...]}
```

---

## MCP

vllm-mlx 내장 MCP(Model Context Protocol) 연동.

### `GET /v1/mcp/tools`

연결된 MCP 서버의 도구 목록.

**응답:**
```json
{
  "tools": [
    {"name": "tool_name", "description": "...", "server": "server_name"}
  ]
}
```

### `GET /v1/mcp/servers`

연결된 MCP 서버 목록.

### `POST /v1/mcp/execute`

MCP 도구 직접 실행.

**요청:**
```json
{
  "server": "server_name",
  "tool": "tool_name",
  "arguments": {"key": "value"}
}
```

---

## 임베딩

### `POST /v1/embeddings`

텍스트 임베딩 벡터 생성.

**요청:**
```json
{
  "model": "mlx-community/gemma-4-26b-a4b-it-4bit",
  "input": "임베딩할 텍스트"
}
```

**응답:**
```json
{
  "object": "list",
  "data": [{"object": "embedding", "index": 0, "embedding": [...]}],
  "usage": {"prompt_tokens": 5, "total_tokens": 5}
}
```

---

## 재순위

### `POST /v1/rerank`

문서 재순위 (Reranking).

**요청:**
```json
{
  "model": "mlx-community/gemma-4-26b-a4b-it-4bit",
  "query": "검색 쿼리",
  "documents": ["문서1", "문서2", "문서3"]
}
```

---

## 캐시

### `GET /v1/cache/stats`
캐시 통계 조회.

### `DELETE /v1/cache`
전체 캐시 삭제.

### `DELETE /v1/cache/prefix`
prefix 캐시 삭제.

---

## SDK 연동 예시

### Python (OpenAI SDK)

```python
from openai import OpenAI

client = OpenAI(base_url="http://localhost:8000/v1", api_key="dummy")

# 채팅
response = client.chat.completions.create(
    model="mlx-community/gemma-4-26b-a4b-it-4bit",
    messages=[{"role": "user", "content": "안녕하세요!"}]
)

# STT
with open("audio.wav", "rb") as f:
    result = client.audio.transcriptions.create(file=f, model="whisper-1", language="ko")

# TTS (Kokoro)
response = client.audio.speech.create(
    model="kokoro", input="Hello!", voice="af_heart"
)
response.stream_to_file("output.wav")
```

### Python (Anthropic SDK)

```python
import anthropic

client = anthropic.Anthropic(
    base_url="http://localhost:8000",
    api_key="dummy"
)

response = client.messages.create(
    model="mlx-community/gemma-4-26b-a4b-it-4bit",
    max_tokens=500,
    messages=[{"role": "user", "content": "안녕하세요!"}]
)
```

### Claude Code 연동

```bash
ANTHROPIC_BASE_URL=http://localhost:8000 \
ANTHROPIC_API_KEY=dummy \
claude --model mlx-community/gemma-4-26b-a4b-it-4bit
```

### curl — 한국어 TTS (Voice Cloning)

```bash
# 본인 목소리
curl http://localhost:8000/v1/audio/speech \
  -H "Content-Type: application/json" \
  -d '{"model":"chatterbox-multilingual","input":"안녕하세요.","voice":"wc","lang_code":"ko","response_format":"wav"}' \
  -o output.wav

# IU 목소리
curl http://localhost:8000/v1/audio/speech \
  -H "Content-Type: application/json" \
  -d '{"model":"chatterbox-multilingual","input":"안녕하세요.","voice":"iu","lang_code":"ko","response_format":"wav"}' \
  -o output.wav
```
