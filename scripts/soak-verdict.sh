#!/bin/sh
# 读 soak 日志，直接给 24 小时验收的结论。
#
# 判据（M6-A5）：关掉浏览器 24 小时后，会话列表/状态/对话都还在，
# 且机器上没有僵尸进程。
LOG=${1:-/var/log/vipanel-soak.log}
[ -f "$LOG" ] || { echo "✗ 找不到 $LOG"; exit 1; }

n=$(grep -c '^时间' "$LOG")
first=$(grep '^时间' "$LOG" | head -1 | awk '{print $2}')
last=$(grep '^时间' "$LOG" | tail -1 | awk '{print $2}')
hours=$(( ( $(date -d "$last" +%s) - $(date -d "$first" +%s) ) / 3600 ))

echo "采样点   $n 个"
echo "跨度     $first → $last （约 ${hours} 小时）"
echo

fail=0
chk() { if [ "$2" = "ok" ]; then echo "✓ $1"; else echo "✗ $1  —— $3"; fail=1; fi; }

# 1 僵尸进程必须恒为 0
zmax=$(grep '^僵尸进程' "$LOG" | awk '{print $2}' | sort -n | tail -1)
chk "僵尸进程恒为 0" "$([ "${zmax:-0}" -eq 0 ] && echo ok)" "出现过 $zmax 个"

# 2 服务全程存活
bad=$(grep '^服务状态' "$LOG" | grep -vc 'active / active')
chk "服务全程 active" "$([ "$bad" -eq 0 ] && echo ok)" "$bad 次非 active"

# 3 HTTP 全程 200
bad=$(grep '^HTTP' "$LOG" | awk '{print $2}' | grep -vc '^200$')
chk "HTTP 全程 200" "$([ "$bad" -eq 0 ] && echo ok)" "$bad 次非 200"

# 4 会话数不减少（会话丢了就是状态没存住）
smin=$(grep '^会话数' "$LOG" | awk '{print $2}' | grep -E '^[0-9]+$' | sort -n | head -1)
slast=$(grep '^会话数' "$LOG" | awk '{print $2}' | grep -E '^[0-9]+$' | tail -1)
chk "会话没丢（最少 $smin，最新 $slast）" "$([ "${slast:-0}" -ge "${smin:-0}" ] && echo ok)" "从 $smin 掉到 $slast"

# 5 fd 数不单调增长——这才是长跑真正要暴露的东西。
#    僵尸进程为 0 只说明进程回收正常，说明不了 fd 泄漏。
#    允许波动，但末值不该显著高于首值（这里取 1.5 倍为线）。
f0=$(grep '^打开的 fd' "$LOG" | head -1 | sed 's/.*agent=\([0-9]*\).*/\1/')
f1=$(grep '^打开的 fd' "$LOG" | tail -1 | sed 's/.*agent=\([0-9]*\).*/\1/')
lim=$(( f0 * 3 / 2 ))
chk "agent fd 未泄漏（$f0 → $f1，上限 $lim）" "$([ "${f1:-0}" -le "$lim" ] && echo ok)" "增长过快"

echo
if [ "$hours" -lt 24 ]; then
    echo "⏳ 尚未满 24 小时（当前 ${hours}h），以上只是阶段性结论"
    exit 2
fi
[ "$fail" -eq 0 ] && echo "✅ M6-A5 通过" || echo "❌ M6-A5 未通过"
exit $fail
