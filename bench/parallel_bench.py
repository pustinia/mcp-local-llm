#!/usr/bin/env python3
"""
병렬 요청 성능 테스트
- N개 요청을 동시에 전송해서 throughput, latency, 성공률 측정
- 사용법: python3 parallel_bench.py [concurrency] [max_tokens]
"""
import sys
import time
import json
import threading
import subprocess
import urllib.request
from concurrent.futures import ThreadPoolExecutor, as_completed
from dataclasses import dataclass, field

BASE_URL  = "http://localhost:8000"
MODEL     = "mlx-community/gemma-4-26b-a4b-it-4bit"

PROMPTS = [
    "지구에서 달까지의 거리를 한 문장으로 설명해.",
    "Python으로 피보나치 수열을 출력하는 코드를 작성해.",
    "인공지능이 인류에게 미치는 영향을 세 줄로 요약해.",
    "대한민국의 수도는 어디야? 한 문장으로만 답해.",
    "HTTP와 HTTPS의 차이를 간단히 설명해.",
    "Go 언어의 장점 세 가지를 나열해.",
    "머신러닝과 딥러닝의 차이를 짧게 설명해.",
    "TCP와 UDP의 차이를 한 문장으로 설명해.",
]

@dataclass
class Result:
    idx:        int
    success:    bool
    elapsed:    float = 0.0
    tokens_out: int   = 0
    error:      str   = ""

def call_llm(idx: int, prompt: str, max_tokens: int) -> Result:
    body = json.dumps({
        "model": MODEL,
        "messages": [{"role": "user", "content": prompt}],
        "max_tokens": max_tokens,
    }).encode()
    req = urllib.request.Request(
        f"{BASE_URL}/v1/chat/completions",
        data=body,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    t0 = time.time()
    try:
        with urllib.request.urlopen(req, timeout=300) as resp:
            data = json.loads(resp.read())
        elapsed = time.time() - t0
        content = data["choices"][0]["message"]["content"]
        # 토큰 수: usage 있으면 사용, 없으면 단어 수로 추정
        tokens_out = data.get("usage", {}).get("completion_tokens", len(content.split()))
        return Result(idx=idx, success=True, elapsed=elapsed, tokens_out=tokens_out)
    except Exception as e:
        return Result(idx=idx, success=False, elapsed=time.time()-t0, error=str(e)[:80])

def sample_gpu() -> dict:
    out = subprocess.run(
        ["ioreg", "-r", "-d", "1", "-c", "AGXAccelerator"],
        capture_output=True, text=True
    ).stdout
    stats = {}
    for line in out.splitlines():
        if "PerformanceStatistics" in line:
            for key, name in [
                ('"Device Utilization %"=', "gpu_util"),
                ('"In use system memory"=', "gpu_mem_b"),
            ]:
                if key in line:
                    val = line.split(key)[1].split(",")[0].split("}")[0]
                    stats[name] = int(val)
    return stats

def run_bench(concurrency: int, max_tokens: int):
    prompts = [PROMPTS[i % len(PROMPTS)] for i in range(concurrency)]

    print(f"\n{'='*55}")
    print(f"  동시 요청: {concurrency}  |  max_tokens: {max_tokens}")
    print(f"{'='*55}")

    # GPU 모니터링 스레드
    gpu_samples = []
    stop_event = threading.Event()
    def gpu_monitor():
        while not stop_event.is_set():
            s = sample_gpu()
            if s:
                gpu_samples.append(s)
            time.sleep(1)
    mon = threading.Thread(target=gpu_monitor, daemon=True)
    mon.start()

    t_start = time.time()
    results = []
    with ThreadPoolExecutor(max_workers=concurrency) as ex:
        futures = {ex.submit(call_llm, i, p, max_tokens): i
                   for i, p in enumerate(prompts)}
        for fut in as_completed(futures):
            results.append(fut.result())
    total_elapsed = time.time() - t_start

    stop_event.set()

    # 결과 집계
    ok  = [r for r in results if r.success]
    err = [r for r in results if not r.success]
    if ok:
        latencies   = sorted(r.elapsed for r in ok)
        avg_lat     = sum(latencies) / len(latencies)
        p50         = latencies[len(latencies)//2]
        p95         = latencies[int(len(latencies)*0.95)]
        total_toks  = sum(r.tokens_out for r in ok)
        throughput  = total_toks / total_elapsed
    else:
        avg_lat = p50 = p95 = total_toks = throughput = 0

    gpu_peak = max((s.get("gpu_util", 0) for s in gpu_samples), default=0)
    gpu_mem  = max((s.get("gpu_mem_b", 0) for s in gpu_samples), default=0) // (1024**2)

    print(f"  완료: {len(ok)}/{concurrency}  실패: {len(err)}")
    print(f"  전체 소요    : {total_elapsed:.1f}s")
    print(f"  평균 latency : {avg_lat:.1f}s")
    print(f"  P50 / P95    : {p50:.1f}s / {p95:.1f}s")
    print(f"  Throughput   : {throughput:.1f} tokens/sec (전체)")
    print(f"  Peak GPU     : {gpu_peak}%  |  GPU mem: {gpu_mem} MB")
    if err:
        for r in err:
            print(f"  [ERROR #{r.idx}] {r.error}")

    return {
        "concurrency": concurrency, "max_tokens": max_tokens,
        "success": len(ok), "fail": len(err),
        "total_elapsed": round(total_elapsed, 1),
        "avg_latency": round(avg_lat, 1),
        "p50": round(p50, 1), "p95": round(p95, 1),
        "throughput_tps": round(throughput, 1),
        "gpu_peak_pct": gpu_peak, "gpu_mem_mb": gpu_mem,
    }

if __name__ == "__main__":
    concurrency = int(sys.argv[1]) if len(sys.argv) > 1 else None
    max_tokens  = int(sys.argv[2]) if len(sys.argv) > 2 else 500

    print("Local LLM 병렬 처리 벤치마크")
    print(f"Model: {MODEL}  |  max_tokens: {max_tokens}")

    if concurrency:
        run_bench(concurrency, max_tokens)
    else:
        # 1 → 2 → 4 → 8 순차 테스트
        all_results = []
        for c in [1, 2, 4, 8]:
            r = run_bench(c, max_tokens)
            all_results.append(r)
            time.sleep(3)  # 서버 안정화 대기

        print(f"\n{'='*55}")
        print("  종합 요약")
        print(f"{'='*55}")
        print(f"{'동시':>4} {'성공':>5} {'전체':>7} {'평균Lat':>8} {'TPS':>8} {'GPU%':>6} {'GPUmem':>8}")
        print("-" * 55)
        for r in all_results:
            print(f"{r['concurrency']:>4} "
                  f"{r['success']:>3}/{r['concurrency']:<2} "
                  f"{r['total_elapsed']:>6.1f}s "
                  f"{r['avg_latency']:>7.1f}s "
                  f"{r['throughput_tps']:>7.1f} "
                  f"{r['gpu_peak_pct']:>6}% "
                  f"{r['gpu_mem_mb']:>6}MB")
