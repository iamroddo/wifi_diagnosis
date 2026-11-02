#!/bin/sh
# Restore host-to-macvlan connectivity for the Synology reverse proxy.
# Configuration is read from /usr/local/etc/wifi-diag-macvlan.conf.

set -eu

CONFIG=/usr/local/etc/wifi-diag-macvlan.conf

[ -r "$CONFIG" ] || exit 0
. "$CONFIG"

case "${1:-start}" in
  start)
    if ! ip link show "$HOST_INTERFACE" >/dev/null 2>&1; then
      ip link add "$HOST_INTERFACE" link "$PARENT_INTERFACE" type macvlan mode bridge
    fi

    if ! ip -o addr show dev "$HOST_INTERFACE" | grep -Fq "$HOST_IP/32"; then
      ip addr add "$HOST_IP/32" dev "$HOST_INTERFACE"
    fi

    ip link set "$HOST_INTERFACE" up
    ip route replace "$CONTAINER_IP/32" dev "$HOST_INTERFACE"
    ;;
  stop)
    ip link delete "$HOST_INTERFACE" 2>/dev/null || true
    ;;
  *)
    echo "Usage: $0 {start|stop}" >&2
    exit 2
    ;;
esac
