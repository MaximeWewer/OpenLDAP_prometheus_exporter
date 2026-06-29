#!/bin/sh
# node_exporter textfile collector: slapd file-descriptor usage.
#
# slapd's FD count is the real connection ceiling and is NOT exposed via
# cn=Monitor. This script emits it for the node_exporter textfile collector.
#
# Install (host slapd):
#   node_exporter --collector.textfile.directory=/var/lib/node_exporter/textfile
#   cron/systemd-timer every 1m, writing atomically:
#     /path/slapd-fd.sh > /var/lib/node_exporter/textfile/slapd_fd.prom.$$ \
#       && mv /var/lib/node_exporter/textfile/slapd_fd.prom.$$ \
#             /var/lib/node_exporter/textfile/slapd_fd.prom
#
# Containerized slapd: run inside the slapd container's PID namespace (its
# pidof sees slapd as PID 1), or point pidof at the host PID of the container's
# slapd. cAdvisor's container_file_descriptors is an alternative source.
set -eu

pid=$(pidof slapd 2>/dev/null | awk '{print $1}')
if [ -z "${pid:-}" ]; then
  echo '# HELP slapd_up slapd process found (1) or not (0).'
  echo '# TYPE slapd_up gauge'
  echo 'slapd_up 0'
  exit 0
fi

open=$(ls "/proc/${pid}/fd" 2>/dev/null | wc -l)
# Soft "Max open files" from the kernel limits file (column 4 = soft limit).
max=$(awk '/Max open files/ {print $4}' "/proc/${pid}/limits" 2>/dev/null)
[ -n "${max:-}" ] || max=0

cat <<EOF
# HELP slapd_open_fds Open file descriptors held by slapd.
# TYPE slapd_open_fds gauge
slapd_open_fds ${open}
# HELP slapd_max_fds Soft RLIMIT_NOFILE for slapd.
# TYPE slapd_max_fds gauge
slapd_max_fds ${max}
# HELP slapd_up slapd process found (1) or not (0).
# TYPE slapd_up gauge
slapd_up 1
EOF
