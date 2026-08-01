#!/bin/sh
# 长期运行观测。在目标机器上执行，打印一份可比对的快照。
#
# 判据（对应 M6-A5）：会话列表 / 状态 / 对话都还在，且没有僵尸进程。
# 这个脚本只负责取样，比对由跑它的人做——基线和终值各跑一次即可。
echo "时间        $(date -Is)"
echo "运行时长    $(systemctl show vipanel-core -p ActiveEnterTimestamp --value)"
echo "服务状态    $(systemctl is-active vipanel-agent) / $(systemctl is-active vipanel-core)"
echo "HTTP        $(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:9999/)"
echo "僵尸进程    $(ps -eo stat --no-headers | awk '$1 ~ /^Z/ {n++} END {print (n?n:0)}')"
echo "agent 子进程 $(ps -eo ppid --no-headers | grep -c "^ *$(pgrep -x 1panel-agent | head -1)$")"
echo "会话数      $(sqlite3 /opt/1panel/db/agent.db 'select count(*) from vi_sessions' 2>/dev/null || echo '(需 sqlite3)')"
echo "内存 RSS    core=$(ps -o rss= -p $(pgrep -x 1panel-core|head -1) 2>/dev/null)KB agent=$(ps -o rss= -p $(pgrep -x 1panel-agent|head -1) 2>/dev/null)KB"
echo "打开的 fd   core=$(ls /proc/$(pgrep -x 1panel-core|head -1)/fd 2>/dev/null|wc -l) agent=$(ls /proc/$(pgrep -x 1panel-agent|head -1)/fd 2>/dev/null|wc -l)"
