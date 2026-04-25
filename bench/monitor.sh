#!/bin/bash
# 지정 PID의 CPU/MEM을 1초마다 샘플링해서 파일에 기록
# 사용법: ./monitor.sh <PID> <output_file>
PID=$1
OUT=$2
echo "timestamp,pcpu,pmem,rss_mb" > "$OUT"
while kill -0 "$PID" 2>/dev/null; do
  ts=$(date +%s)
  read pcpu pmem rss <<< $(ps -p "$PID" -o pcpu=,pmem=,rss= 2>/dev/null)
  rss_mb=$(echo "scale=1; ${rss:-0} / 1024" | bc)
  echo "$ts,$pcpu,$pmem,$rss_mb" >> "$OUT"
  sleep 1
done
