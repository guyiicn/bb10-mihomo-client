#!/bin/sh
# Root/app split test: helper as root, Mihomo core as uid 100 (the proxy
# app's uid). Same routes as run-range.sh, installed by the helper.
trap "" HUP INT TERM
W=/var/tmp/qnx-tun-split-20261115
B=/accounts/1000/shared/misc/berrycore
P=198.18.255.2
NS=
for l in `getconf _CS_RESOLVE`; do NS="$NS ${l#nameserver_}"; done
tunroutes() { echo "routes via tun3 / fake range: `netstat -rn | grep -c -E 'tun3|198\.19'`"; }
resolve() { ping -c 1 -w 3 example.com 2>&1 | grep -i -E "^PING|unknown|cannot"; }
corepids() { pidin -F '%a %N' | grep mihomo-tun | while read p n; do echo $p; done; }
fallback() {
  echo "=== fallback cleanup (should find nothing to do)"
  for p in `corepids`; do kill -9 $p; done
  [ -n "$HPID" ] && kill -9 $HPID 2>/dev/null
  route -n delete -net 198.19.0.0 -netmask 255.255.0.0 $P >/dev/null 2>&1 && echo "HAD TO DELETE RANGE ROUTE"
  for n in $NS; do route -n delete -host $n $P >/dev/null 2>&1 && echo "HAD TO DELETE NS ROUTE"; done
  ifconfig tun3 inet 198.18.255.1 delete 2>/dev/null; ifconfig tun3 down
  chown root:nto /dev/tun3; chmod 600 /dev/tun3
  ls -l /dev/tun3; ifconfig tun3; tunroutes
  echo "processes left: `pidin -F '%N' | grep -c -E 'mihomo-tun|tun-helper'`"
}
rm -rf $W/home $W/sock; mkdir -p $W/home; cp $W/config-hijack.yaml $W/home/config.yaml
chown -R 100 $W/home; chmod 755 $W; chmod 700 $W/home
echo "=== start helper (root)"
$W/tun-helper serve 100 $W/sock/h.sock > $W/helper.log 2>&1 &
HPID=$!
sleep 2; cat $W/helper.log; ls -l /dev/tun3
echo "=== start core as uid 100 under the helper client"
on -u 100:902 -e BB10_TUN_EXPERIMENT=1 $W/tun-helper run $W/sock/h.sock 5s -- $W/mihomo-tun -d $W/home > $W/core.log 2>&1 &
i=0; while [ $i -lt 20 ]; do grep -q "^helper:" $W/core.log && break; sleep 1; i=`expr $i + 1`; done
grep -E "^helper:|^tun-helper|Tun adapter|level=(error|fatal)" $W/core.log
pidin users | grep -E "mihomo-tun|tun-helper"
tunroutes
echo "=== app-side view"
resolve
$B/bin/curl -sS --noproxy "*" --cacert $B/certs/cacert.pem -m 12 -o /dev/null -w "http=%{http_code} remote=%{remote_ip} local=%{local_ip}\n" https://example.com/ 2>&1
echo "=== crash test: kill -9 the core"
for p in `corepids`; do kill -9 $p; done
sleep 3
tunroutes; resolve
cat $W/helper.log
echo "=== stop helper (SIGTERM)"
kill $HPID; sleep 2; cat $W/helper.log | grep restore; ls -l /dev/tun3
HPID=
grep -E "\[TCP\].*-->" $W/core.log
fallback
