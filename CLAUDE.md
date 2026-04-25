# CLAUDE.md

AI 코딩 어시스턴트를 위한 내부 문서. README.md는 사람 독자용이고 이 파일은 코드 작업 전 컨텍스트 파악용.

## Project layout

```
cmd/mcp-local-llm/main.go   # 엔트리포인트: 버전 플래그, 환경변수 파싱, MCP 서버 등록
internal/llm/client.go      # HTTP 클라이언트: OpenAI-compatible /v1/chat/completions 호출
Makefile                    # build / clean 타겟
.env.example                # 환경변수 목록 참조용 (바이너리는 파일 읽지 않음)
```

## Build & run

```bash
make build                          # → bin/mcp-local-llm
make clean                          # bin/ 삭제
./bin/mcp-local-llm -version        # 버전 확인
```

버전(`main.version`)과 빌드 시각(`main.buildTime`)은 `-ldflags`로 컴파일 타임에 주입된다.
git tag가 없으면 `version=dev`.

## Key conventions

- **설정은 환경변수로만** — 플래그는 `-version` 하나뿐. 새 설정을 추가할 때 flags를 늘리지 말고 `envOr()` / `os.Getenv()` 패턴을 사용한다.
- **`internal/` 패키지는 외부 노출 없음** — `llm.Input`, `llm.Client` 등은 이 모듈 안에서만 쓴다.
- **에러 래핑** — `fmt.Errorf("context: %w", err)` 패턴을 따른다.
- **MCP 에러 반환** — 도구 레벨 에러는 `log.Fatal`이 아닌 `CallToolResult{IsError: true}`로 반환한다.

## Adding a new MCP tool

1. 필요하면 `internal/` 아래에 새 패키지 또는 함수 추가
2. `cmd/mcp-local-llm/main.go`에서 `mcp.AddTool()` 호출 추가
3. 입력 구조체는 `jsonschema` 태그로 Claude에 파라미터 설명 제공

## Testing & debugging

로컬 LLM 서버 없이 MCP 프로토콜만 검증:

```bash
npx @modelcontextprotocol/inspector ./bin/mcp-local-llm
```

stdin으로 JSON-RPC 직접 전달도 가능:

```bash
echo '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' | ./bin/mcp-local-llm
```
