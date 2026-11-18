#!/bin/sh
# Whole fake-ip range + DNS hijack test. Routes added (FIB 0, IPv4 only):
#   198.19.0.0/16 and one /32 per system nameserver -> tun3.
# Never touches default routes, confstr, IPv6, PF, APN or vpn_pps.
trap "" HUP INT TERM
D=/var/tmp/qnx-tun-e2e-20261115
L=198.18.255.1
P=198.18.255.2
B=/accounts/1000/shared/misc/berrycore
CA=$B/certs/cacert.pem
NS=
for l in `getconf _CS_RESOLVE`; do NS="$NS ${l#nameserver_}"; done
MPID=
tunroutes() { echo "routes via tun3 / fake range: `netstat -rn | grep -c -E 'tun3|198\.19'`"; }
cleanup() {
  echo "=== cleanup"
  route -n delete -net 198.19.0.0 -netmask 255.255.0.0 $P >/dev/null 2>&1
  for n in $NS; do route -n delete -host $n $P >/dev/null 2>&1; done
  [ -n "$MPID" ] && { kill -9 $MPID 2>/dev/null; }
  ifconfig tun3 inet $L delete 2>/dev/null; ifconfig tun3 down
  ifconfig tun3; tunroutes
  echo "mihomo processes left: `pidin -F '%N' | grep -c mihomo-tun`"
}
echo "nameservers: $NS"
case "$NS" in *:*|"") echo "unexpected nameserver list, abort"; exit 1;; esac
rm -rf $D/home; mkdir -p $D/home; cp $D/config-hijack.yaml $D/home/config.yaml
BB10_TUN_EXPERIMENT=1 $D/mihomo-tun -d $D/home > $D/mihomo.log 2>&1 &
MPID=$!
i=0; while [ $i -lt 15 ]; do grep -q -i "tun adapter listening" $D/mihomo.log && break; kill -0 $MPID 2>/dev/null || break; sleep 1; i=`expr $i + 1`; done
if ! grep -q -i "tun adapter listening" $D/mihomo.log; then echo "mihomo TUN did not start"; grep -i -E "error|fatal" $D/mihomo.log; cleanup; exit 1; fi
ifconfig tun3 inet $L $P netmask 255.255.255.255 up; echo "ifconfig rc=$?"
echo "=== before routes: system resolver"
ping -c 1 -w 3 example.com 2>&1 | grep -i -E "^PING|unknown|cannot"
echo "=== add routes"
route -n add -net 198.19.0.0 -netmask 255.255.0.0 $P; echo "range rc=$?"
for n in $NS; do route -n add -host $n $P; echo "ns rc=$?"; done
tunroutes
echo "=== system resolver (libsocket) now"
ping -c 1 -w 3 example.com 2>&1 | grep -i -E "^PING|unknown|cannot"
echo "=== curl, no proxy settings"
for u in https://example.com/ https://www.baidu.com/ https://www.qq.com/; do
  $B/bin/curl -sS --noproxy "*" --cacert $CA -m 12 -o /dev/null -w "$u http=%{http_code} remote=%{remote_ip} local=%{local_ip}\n" $u 2>&1
done
echo "=== observe background traffic 15s"
sleep 15
echo "=== mihomo saw"
echo "dns hijack lines: `grep -c 'hijack' $D/mihomo.log`"
echo "tcp conns: `grep -c '\[TCP\].*-->' $D/mihomo.log`  udp conns: `grep -c '\[UDP\].*-->' $D/mihomo.log`"
echo "=== crash test: kill -9"
kill -9 $MPID; sleep 2
tunroutes; ifconfig tun3
ping -c 1 -w 3 example.com 2>&1 | grep -i -E "^PING|unknown|cannot"
cleanup
