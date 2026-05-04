# mcp-local-llm — 본질적 개선 후보

이 문서는 MCP 서버 툴 호출 경로와 `internal/llm` 클라이언트에서 **성능·신뢰성·메모리**에 실질적 영향이 있는 개선 아이디어를 정리한 것이다. 순서만 바꾸는 수준의 리팩터링은 제외했다.

---

## 1. `usage.json` 매 호출 디스크 저장 — 핵심 경로의 불필요한 I/O

**위치:** [`cmd/mcp-local-llm/main.go`](../cmd/mcp-local-llm/main.go) — `call_local_llm` 툴 핸들러에서 매 LLM 호출마다 `stats.save(usagePath)` 호출.

**현재 동작:** `WriteFile(.tmp)` + `Rename` 등이 응답 반환 직전에 동기 실행된다.

**문제:**

- 동시 호출 시 `save()`가 뮤텍스 해제 뒤 파일 I/O를 수행하면, 여러 고루틴이 같은 파일에 경쟁적으로 쓰기 → **마지막 쓰기 승, 카운트 손실 가능**.
- 핵심 경로에서 저장 오류를 `_`로 무시해 **안전성·관측 가능성**이 낮다.

**개선 방향:**

- **디바운스 / 주기 저장:** 별도 고루틴에서 N초마다, 변경이 있을 때만 저장. 툴 핸들러 핵심 경로에서는 `save` 제거.
- **Graceful shutdown:** `signal.Notify(SIGINT/SIGTERM)` 후 마지막 flush. 디바운스 도입 시 프로세스 종료 시 통계 유실 방지에 필요.

---

## 2. 이미지 base64 인코딩의 메모리 이중·삼중 점유

**위치:** [`internal/llm/client.go`](../internal/llm/client.go) — `readImageAsDataURI` (파일 전체 `io.ReadAll` → `base64.StdEncoding.EncodeToString` → `chatRequest` JSON marshal).

**현재 동작:** 큰 이미지일수록 원본 바이트 + base64 문자열 + JSON 바디까지 피크 메모리가 커진다 (예: 5MB 이미지면 대략 15MB+ 수준까지 가능).

**개선 방향:**

- **`io.Pipe` + `base64.NewEncoder`** 로 HTTP 요청 본문에 스트리밍하여 중간 대형 문자열 할당을 줄인다.
- 또는 최소한 **사전 크기 계산 후 단일 `[]byte` 버퍼**에 인코딩해 `EncodeToString`의 추가 할당을 줄인다.
- **`LOCAL_LLM_MAX_IMAGES`** 기본값(예: 5)과 함께 쓰일 때 효과가 배로 커진다.

---

## 3. 트랜션트 오류 재시도 부재 — 신뢰성

**위치:** [`internal/llm/client.go`](../internal/llm/client.go) — `http.Client.Do` 실패 또는 5xx 응답 시 즉시 에러 반환.

**문제:** vllm-mlx가 일시적으로 불안정할 때(5xx, KV/OOM 직후, 연결 거절 등) MCP 사용자에게 바로 실패로 전달된다.

**개선 방향:**

- **Bounded retry:** 5xx, 일시적 `net.Error`, `connection refused` 등에 한해 최대 2회, 200ms → 1s 정도 백오프.
- POST이지만 동일 프롬프트 재시도는 운영상 허용 가능한 경우가 많다(출력은 원래 비결정적).
- **`context.Context` 취소** 시 재시도 중단.

---

## 4. 입력 토큰 사전 추정 — 서버 OOM/한도 초과 완화

**배경:** 긴 입력 + 큰 `max_tokens` 조합에서 vllm-mlx(Metal) OOM 등이 클라이언트에서 사전에 막기 어렵다.

**위치:** [`internal/llm/client.go`](../internal/llm/client.go) — `Call()` 진입부 근처.

**개선 방향:**

- `prompt` + `input_files` 내용 + `system` 등 **합산 문자 수**로 토큰을 거칠게 추정 (예: `chars / 3.5`).
- **`LOCAL_LLM_SERVER_TOKEN_LIMIT`** (또는 유사 이름) 환경 변수로 서버가 허용하는 **요청 전체 토큰 상한**을 받는다.
- `추정 입력 토큰 + (요청 max_tokens 또는 기본 max_tokens) > 한도`이면 **요청 전 명시적 에러**로 거절해, 서버 프로세스를 죽이는 패턴을 줄인다.

---

## 5. `buildPromptWithFiles` — 한도 초과로 거절될 파일도 전부 읽음

**위치:** [`internal/llm/client.go`](../internal/llm/client.go) — `buildPromptWithFiles`: `os.ReadFile` 후 누적 바이트가 `maxBytes`를 넘으면 에러.

**문제:** 단일 대용량 파일이면 **전부 읽은 뒤**에야 초과를 감지할 수 있어 I/O·메모리 낭비.

**개선 방향:**

- **1패스:** `os.Stat`으로 각 파일 크기 합산 → `maxBytes` 초과 시 즉시 에러.
- **2패스:** 통과한 경우에만 `os.ReadFile`로 내용 로드.

---

## 6. 세마포어 획득 순서 — 저비용 검증이 LLM 슬롯 뒤로 밀림

**위치:** [`internal/llm/client.go`](../internal/llm/client.go) — `Call()`에서 동시성 세마포어를 먼저 잡고, 그 다음 `validatePath` 등을 수행.

**문제:** 잘못된 경로 등 **즉시 실패** 케이스가 활성 LLM 요청 뒤에 대기할 수 있다 (`LOCAL_LLM_MAX_CONCURRENT` 사용 시).

**개선 방향:**

- 경로 검증·파일 존재 등 **저비용·결정적 검증**을 세마포어 획득 **이전**에 수행하고, HTTP 호출 직전에만 세마포어를 잡는 구조로 재배치한다.

---

## 7. `max_tokens`·로컬 추론 한계 — 출력 연속 완성(continuation)과 입력 분할의 구분

**위치(예상):** [`internal/llm/client.go`](../internal/llm/client.go) — `Call()` 및 (도입 시) 내부 전용 연속 완성 헬퍼.

**현재 동작:**

- `Call()`은 **단일** `POST /v1/chat/completions` 호출이다. `max_tokens`는 그 한 번의 **출력 상한**만 지정한다.
- 요청 `max_tokens`가 **서버가 허용하는 최대값을 초과**하면(로컬 MLX 스택 등에서 관측) **HTTP 400**으로 거절되며, 클라이언트는 **자동 분할·재요청을 하지 않는다**.
- `max_tokens`가 한도 **이하**이면 생성은 해당 토큰에서 **잘린 채** 응답으로 돌아오고, **추가 라운드 없이** 그 문자열이 그대로 반환 경로로 이어진다.

**문제:**

- 로컬 LLM의 성능·메모리 제약 때문에 운영상 `max_tokens`를 **낮게** 두는 경우가 많고, 그때 **긴 답**은 한 번의 완성으로는 끝까지 나오지 않는다.
- 사용자가 기대하는 “청킹”에 가깝게 해결하려면, 같은 논리적 요청을 **여러 번의 completion으로 이어 붙이는 출력 연속 완성(continuation)** 이 자연스럽다. 이는 **입력**을 잘라 여러 번 보내는 것과 목적·난이도가 다르다(아래 표).

**개선 방향 — 출력(1차 범위):**

- **Continuation 루프:** 1차 응답 후, 메시지 히스토리에 `assistant`(지금까지의 생성물)를 넣고 `user`에 고정 문구(예: 이어서 출력, 잘린 지점부터 계속)를 추가해 **같은 모델에 재요청**한다. 구현은 공개 `Call`을 N번 호출하거나, 내부 전용 헬퍼로 묶을 수 있다.
- **중단 조건:** 최대 **라운드 수**, **누적 출력 토큰·문자 상한**, 연속 **빈 증분** 등. `finish_reason`이나 `usage.completion_tokens`와 요청 `max_tokens`의 관계로 “한도에 도달했는지”를 보조 판단할 수 있으나, **MLX 등에서는 `finish_reason`이 항상 `length`로 오지 않을 수 있어** 단일 신호에 의존하지 않는 편이 안전하다.
- **Gemma thinking:** 라운드마다 `<|channel>thought…<channel|>` 블록이 붙을 수 있다. **라운드별로 thinking 제거(현재 또는 향후 `FilterThinking`과 동등한 처리) 후 문자열만 누적**하거나, continuation 전용 시스템/지시문으로 형식을 안정화하는 방안을 검토한다.
- **환경 변수(제안):** 예) `LOCAL_LLM_OUTPUT_CHUNK_ENABLED`(기본 `false`), `LOCAL_LLM_OUTPUT_CHUNK_MAX_ROUNDS`. 기본은 끄고 **옵트인**이 안전하다(지연·부하·비결정적 출력 증가).
- **MCP 계약:** `call_local_llm` **한 번**의 툴 호출이 내부적으로 **N번** HTTP를 사용할 수 있게 되므로, README·툴 설명에서 기대치를 맞춘다.

**입력 토큰도 같은 “청킹”으로 처리해야 하는가?**

| 구분 | 자동 청킹(연속 호출)이 직접 맞는가 | 비고 |
|------|-------------------------------------|------|
| **출력** (`max_tokens` 등으로 **생성이 잘림**) | **예** — continuation이 정면 해법 | §7의 1차 설계 범위 |
| **입력** (프롬프트+파일이 **모델 컨텍스트 한도**에 근접) | **별도 문제** — 프롬프트를 잘라 **순서 없이** 여러 번만 보내면 최종 답을 합성할 수 없다 | **맵리듀스**(청크 요약 후 최종 합성), **RAG**, **일부만 포함**, 또는 **§4**의 사전 추정·거절과 연계한 운영이 자연스럽다 |
| **입력** (**§5** `buildPromptWithFiles`·`LOCAL_LLM_MAX_INPUT_BYTES` 등 **클라이언트 한도**) | 현재는 **거절**이 맞고, “자동 분할”은 **정책**(파일 순서, 중간 결과 저장 위치)이 필요 | **§5**·**§4**와 같은 축; continuation 메커니즘으로 대체되지 않는다 |

**결론:** 자동 청킹 설계의 **1차 범위는 출력 연속 완성**으로 두고, 입력 한도·컨텍스트 문제는 **§4·§5**와 연계한 **사전 검증**과, 필요 시 **§8**의 **입력 맵리듀스**로 다룬다.

---

## 8. 큰 `input_files`(비이미지) 자동 분할·맵리듀스 분석

**위치(예상):** [`internal/llm/client.go`](../internal/llm/client.go) — `buildPromptWithFiles` 호출 전·대체 경로, 또는 `Call()` 내부에서 파일 합산 크기·타입에 따라 분기.

**배경:** 이미지가 아닌 **텍스트·코드·마크다운** 등은 바이트/문자 경계로 잘라도, 청크마다 부분 분석 후 합치는 **맵리듀스** 패턴이 현실적이다(§7 표의 “입력 컨텍스트” 행과 연결). **이미지**는 한 장 단위가 아니면 의미 단위가 깨져 **본 §8의 기본 대상에서 제외**하는 편이 낫다. **오디오·STT**는 `chat`이 아니라 **`/v1/audio/transcriptions`** 축이므로 **§9**에서 다룬다.

**현재 동작:**

- `input_files`는 전부 읽어 프롬프트에 붙이고 **한 번**만 LLM에 보낸다. `LOCAL_LLM_MAX_INPUT_BYTES` 초과 시 **거절**한다(§5). 모델 컨텍스트·OOM은 클라이언트가 사전에 맞추지 않는다(§4).

**개선 방향:**

- **맵 단계:** 사용자 `prompt`와 “이 파일의 i/N 청크”·겹침(overlap) 옵션을 넣어 **청크당 한 번** completion 호출. 청크 크기는 바이트/문자(구현 단순) 또는 **§4**와 같은 **대략 토큰 추정** 기준 중 선택.
- **리듀스 단계:** 맵 단계 산출(요약·추출)을 모아 **최종 질의 한 번**(또는 계층적 리듀스)으로 전체 답을 생성.
- **한도 정책:** “원본 전체 허용량” vs “청크당 상한”을 env로 분리해, 기존 `LOCAL_LLM_MAX_INPUT_BYTES` 의미와 충돌하지 않게 문서화한다.
- **바이너리:** 압축·실행 파일 등은 텍스트처럼 자른 분석이 무의미할 수 있어 **확장자/스니핑으로 분할 대상 제외**하거나, 분할 시 “원시 바이트 나열”임을 명시하는 등 보수적 기본값을 둔다.
- **환경 변수(제안):** 예) `LOCAL_LLM_INPUT_MAPREDUCE_ENABLED`(기본 `false`), `LOCAL_LLM_INPUT_CHUNK_BYTES`, `LOCAL_LLM_INPUT_CHUNK_OVERLAP_BYTES`, `LOCAL_LLM_INPUT_MAPREDUCE_MAX_CHUNKS`. **옵트인**으로 두어 호출 수·지연·비결정성을 사용자가 감수하도록 한다.
- **§7과의 관계:** 출력 **continuation**과 조합하면 내부 HTTP 횟수가 더 늘어난다. README·툴 설명에 “단일 `call_local_llm`이 다회 호출될 수 있음”을 **입력 분할** 차원에서도 명시한다.

---

## 9. MCP에 STT·TTS 연동 (로컬 `VLLM_API` 오디오 엔드포인트)

**참고 스펙:** [`docs/VLLM_API.md`](VLLM_API.md) — 채팅과 **동일 베이스 URL**(`LOCAL_LLM_BASE_URL`)에서 OpenAI 호환 오디오 API를 제공한다.

**배경:** 음성은 `chat/completions` 텍스트·이미지 경로와 다르다. STT는 **`POST /v1/audio/transcriptions`**(multipart 파일 업로드), TTS는 **`POST /v1/audio/speech`**(JSON 입력·바이너리 응답). 추후 MCP에 도구로 노출할 때 **별도 HTTP 클라이언트 헬퍼**와 **툴 스키마**가 필요하다.

**현재 동작:**

- [`internal/llm/client.go`](../internal/llm/client.go) / `call_local_llm`은 **채팅 완성만** 다룬다. STT·TTS 호출은 없음.

**개선 방향 — STT (음성 → 텍스트):**

- **단일 파일:** `workDir` 하위 오디오 경로 검증(기존 `validatePath` 패턴) 후 `multipart/form-data`로 `file`, `model`(문서상 `whisper-1`), 선택 `language` 전송. 응답 JSON의 `text`를 툴 결과로 반환하거나, 이어서 `Call()`에 넣을 수 있게 **옵션**을 둔다.
- **긴 녹음 분할:** API는 “파일 하나당 한 번 전사” 모델이므로, **클라이언트가** 시간 구간 등으로 오디오를 나눈 뒤 **transcriptions를 N번 호출**하고 문자열을 병합한다(§8 맵 단계와 **오케스트레이션만 유사**, 엔드포인트는 다름). 겹침·문장 경계·최대 세그먼트 수는 env로 제한.
- **환경 변수(제안):** 예) `LOCAL_LLM_STT_CHUNK_SECONDS`, `LOCAL_LLM_STT_MAX_SEGMENTS`, 분할 사용 시 옵트인 플래그. ffmpeg 등 **외부 바이너리 의존** 여부는 구현 시 명시.

**개선 방향 — TTS (텍스트 → 음성):**

- JSON 바디(`model`, `input`, `voice`, `response_format`, `lang_code` 등 — [`VLLM_API.md`](VLLM_API.md) 표 준수)로 **`POST /v1/audio/speech`** 호출, 응답 바이너리를 **`output_file` 동일 규칙**으로 `workDir` 안 경로에 저장하거나, base64/data URL 반환 여부는 툴 설계에서 결정(대용량은 파일 저장이 MCP·Claude 쪽에 유리).
- 채팅 `max_tokens`·thinking 필터와 **무관**; 타임아웃만 공유 가능.

**MCP 설계 선택:**

- **`call_local_llm` 확장**보다 **`call_local_stt` / `call_local_tts`(가칭) 분리**를 권장한다 — 파라미터·응답 형식이 달라 스키마가 단순해지고, 채팅 usage 집계와도 분리하기 쉽다.
- 툴 추가 시 [`cmd/mcp-local-llm/main.go`](../cmd/mcp-local-llm/main.go)의 `AddTool`·usage 저장 경로를 따라가고, [`README.md`](../README.md)·[`.env.example`](../.env.example)에 베이스 URL 재사용과 신규 env를 반영한다 ([`CLAUDE.md`](../CLAUDE.md)).

**§8과의 구분:** §8은 **텍스트 파일** 맵리듀스; 본 §9는 **오디오 → 전사 → (선택) LLM** 파이프라인. 동일 녹음에 대해 “전사만” vs “전사 후 Gemma 분석”은 별 툴 호출 또는 단일 툴의 플래그로 나눌 수 있다.

---

## 구현 시 참고

- 환경 변수 추가 시 [`README.md`](../README.md) Configuration 표와 예시를 갱신한다 ([`CLAUDE.md`](../CLAUDE.md) 워크플로 규칙).
- STT·TTS 도구 도입 시에도 동일하며, **바이너리 응답·multipart 요청**은 Inspector/클라이언트 호환성을 README에 한 줄이라도 적어두면 좋다.
- `LOCAL_LLM_SERVER_TOKEN_LIMIT` 같은 값은 **추정치 기반**이므로 에러 메시지에 “대략적 추정”임을 명시하는 것이 좋다.
- 디바운스 저장 도입 시 **프로세스 강제 종료(SIGKILL)** 에는 flush가 보장되지 않음을 문서에 적어두면 된다.

---

## 10. Tool Calling 지원 — 로컬 LLM에게 Claude 도구 위임

**배경:** vllm-mlx 서버는 OpenAI 호환 `tools` / `tool_choice` 파라미터를 완전히 지원한다(실험 확인). 현재 `call_local_llm`은 텍스트 in/out만 처리하며, `chatResponse.choices[0].message.tool_calls`를 무시한다. 이를 활성화하면 아래 세 가지 패턴으로 **Claude 토큰을 대폭 절약**할 수 있다.

### 세 가지 위임 패턴

**패턴 A — Manager-Worker (로컬 LLM이 툴 선택·판단)**

로컬 LLM이 어떤 툴을 어떤 순서로 호출할지 결정하고, Claude는 툴 실행 릴레이만 담당한다.

```
Claude → call_local_llm(prompt, tools=[Read, Bash, Glob])
로컬LLM → tool_calls: [Read("main.go"), ...]
Claude  → 툴 실행 후 call_local_llm(messages=[...tool_result...])
로컬LLM → 최종 텍스트
```

- **절약 효과:** 파일 20개 분석 기준 Claude 토큰 약 94% 감소 (41,500 → 2,300)
- **적합한 태스크:** 반복 파일 읽기, 코드베이스 탐색, 멀티스텝 분석

**패턴 B — Decision-Secretary (Claude가 툴 지정, 로컬 LLM이 인자 생성+합성)**

`tool_choice`로 Claude가 어떤 툴을 쓸지 지시하고, 로컬 LLM은 인자 생성과 최종 답변 합성을 담당한다.

```
Claude → call_local_llm(prompt, tools=[get_weather], tool_choice="get_weather")
로컬LLM → tool_calls: [get_weather({"city":"서울"})]
Claude  → 툴 실행 후 결과 전달
로컬LLM → 최종 합성 텍스트
```

- **절약 효과:** 단순 태스크 기준 약 63% 감소
- **적합한 태스크:** 특정 데이터 1회 조회 + 긴 합성 출력

**패턴 C — Researcher-Writer (Claude가 수집, 로컬 LLM이 보고서 작성)**

Claude가 WebSearch·Bash·Read 등 자신의 툴로 데이터를 수집하고, 결과를 `messages`에 담아 로컬 LLM에 전달하면 로컬 LLM이 합성만 담당한다.

```
Claude  → WebSearch, Read(코드파일들) 실행
Claude  → call_local_llm(messages=[수집된_데이터], prompt="보고서 작성")
로컬LLM → 최종 보고서
```

- **절약 효과:** 보고서 작성 output 토큰 제거 (Claude output이 가장 비쌈)
- **적합한 태스크:** 조사+합성 분리 가능한 장문 보고서, 릴리즈 노트 분석

### 구현 범위

**`internal/llm/client.go`**

```go
// Input에 추가
Tools      []json.RawMessage `json:"tools,omitempty"`
ToolChoice interface{}       `json:"tool_choice,omitempty"`
Messages   []ChatMessage     `json:"messages,omitempty"` // 멀티턴

// chatResponse에 추가
type toolCall struct {
    ID       string `json:"id"`
    Type     string `json:"type"`
    Function struct {
        Name      string `json:"name"`
        Arguments string `json:"arguments"`
    } `json:"function"`
}
// choices[0].message.tool_calls 파싱

// Call() 반환값 변경
// tool_calls가 있으면 JSON 문자열로 반환 (Claude가 루프 처리)
```

**`cmd/mcp-local-llm/main.go`**

- `call_local_llm` 툴 설명에 tool calling 사용법 추가
- tool_calls 응답 시 JSON 그대로 반환 (Claude가 파싱 후 다음 단계 결정)

### 주의사항

- **멀티턴 루프는 Claude가 담당**: MCP 서버는 무상태(stateless) 유지. 루프 오케스트레이션은 Claude가 `call_local_llm`을 반복 호출하는 방식.
- **`Messages` 파라미터**: system/user/assistant/tool 역할 지원 필요. `Input.System`과 충돌하지 않도록 설계.
- **하위 호환성**: `tools` 없이 호출하면 기존과 동일하게 동작해야 함.
- **Gemma thinking 블록**: tool_calls 응답에도 thinking 블록이 붙을 수 있어 `FilterThinking` 적용 필요.

---

## 관련 파일

| 영역 | 파일 |
|------|------|
| MCP 툴, usage 저장 | `cmd/mcp-local-llm/main.go` |
| HTTP 클라이언트, 프롬프트/이미지/필터 | `internal/llm/client.go` |
| STT·TTS HTTP (예정) | `internal/llm/` 신규 또는 `client.go` 인접 패키지 — §9 |
| Tool calling 지원 (예정) | `internal/llm/client.go`, `cmd/mcp-local-llm/main.go` — §10 |
| 로컬 오디오 API 스펙 | `docs/VLLM_API.md` |
| 기존 단위 테스트 | `internal/llm/client_test.go` |
