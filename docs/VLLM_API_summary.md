# vllm-mlx API 엔드포인트 요약
vllm-mlx 서버에서 제공하는 모든 API 엔드포인트를 카테고리별로 정리한 목록입니다.

| 카테고리 | 메서드 | 엔드포인트 | 역할 (한 줄 요약) |
| :--- | :--- | :--- | :--- |
| **시스템** | GET | `/health` | 서버 상태 확인 |
| **시스템** | GET | `/metrics` | Prometheus 형식 메트릭 조회 |
| **시스템** | GET | `/v1/status` | 모델 로드 상태 및 설정 정보 확인 |
| **모델** | GET | `/v1/models` | 로드된 모델 목록 조회 |
| **채팅 (OpenAI)** | POST | `/v1/chat/completions` | OpenAI 호환 채팅 응답 생성 |
| **채팅 (Anthropic)** | POST | `/v1/messages` | Anthropic 호환 메시지 생성 |
| **채팅 (Anthropic)** | POST | `/v1/messages/count_tokens` | 입력 토큰 수 계산 |
| **텍스트 완성** | POST | `/v1/completions` | 레거시 텍스트 완성 (OpenAI 호환) |
| **STT (음성 인식)** | POST | `/v1/audio/transcriptions` | 음성 파일을 텍스트로 변환 |
| **TTS (음성 합성)** | POST | `/v1/audio/speech` | 텍스트를 음성으로 변환 |
| **TTS (음성 합성)** | GET | `/v1/audio/voices` | 사용 가능한 TTS 목소리 목록 조회 |
| **MCP** | GET | `/v1/mcp/tools` | 연결된 MCP 서버의 도구 목록 조회 |
| **MCP** | GET | `/v1/mcp/servers` | 연결된 MCP 서버 목록 조회 |
| **MCP** | POST | `/v1/mcp/execute` | MCP 도구 직접 실행 |
| **임베딩** | POST | `/v1/embeddings` | 텍스트 임베딩 벡터 생성 |
| **재순위** | POST | `/v1/rerank` | 문서 재순위화 (Reranking) |
| **캐시** | GET | `/v1/cache/stats` | 캐시 통계 조회 |
| **캐시** | DELETE | `/v1/cache` | 전체 캐시 삭제 |
| **캐시** | DELETE | `/v1/cache/prefix` | Prefix 캐시 삭제 |