#include "proxycontroller.h"
#include <bb/data/JsonDataAccess>
#include <bb/cascades/Application>
#include <bb/cascades/ThemeSupport>
#include <bb/cascades/Theme>
#include <bb/cascades/VisualStyle>
#include <QDir>
#include <QFile>
#include <QFileInfo>
#include <QtNetwork/QTcpServer>
#include <QtNetwork/QHostAddress>
#include <QtNetwork/QNetworkRequest>
#include <QtNetwork/QNetworkReply>
#include <QtNetwork/QNetworkProxy>
#include <QRegExp>
#include <QTimer>
#include <QVariant>
#include <QUuid>
#include <QUrl>
#include <algorithm>
#include <vector>
#include <unistd.h>

// AutoRoot's root wrapper. It is a setuid-root ELF on the trusted read-only
// rcfs (/base/bin), world-executable, that runs a root ksh reading its stdin.
// This phone does not honour setuid on the writable filesystem, so the app
// cannot carry its own setuid helper; instead it reaches root exactly the way
// the VoLTE app does, by exec'ing this wrapper and piping it a command. When it
// is absent VPN mode is unavailable and proxy mode is unaffected.
static const char *const kRootWrapper = "/bin/__root";

// Node-sort key at file scope: C++98 forbids a function-local type as a template
// argument to std::stable_sort, so NodeKey/NodeKeyLess live here. tier 0=tested,
// 1=failed, 2=untested; within a tier, lower ms first, src breaks ties (stable).
namespace {
struct NodeKey { int tier; int ms; int src; };
struct NodeKeyLess {
 bool operator()(const NodeKey &a,const NodeKey &b) const {
  if(a.tier!=b.tier)return a.tier<b.tier;
  if(a.ms!=b.ms)return a.ms<b.ms;
  return a.src<b.src;
 }
};
// One prepared connection row + its cumulative download, so the snapshot can be
// stable-sorted by download descending (biggest talkers first) in C++98.
struct ConnRow { qint64 dl; QVariantMap map; };
struct ConnRowMore {
 bool operator()(const ConnRow &a,const ConnRow &b) const { return a.dl>b.dl; }
};
}

ProxyController::ProxyController(QObject *parent) : QObject(parent), m_index(0), m_status(QString::fromUtf8("请选择配置文件")), m_stopRequested(false), m_vpnMode(false), m_splitMode(true), m_vpnUp(false), m_restartingCore(false), m_ctrlPort(0), m_testInFlight(false), m_testOk(0), m_testBad(0), m_testTotal(0), m_sortByLatency(false), m_connCount(0), m_ruleCount(0), m_darkTheme(false) {
 m_netWatch.setInterval(3000);
 connect(&m_netWatch,SIGNAL(timeout()),this,SLOT(onNetWatch()));
 m_connTimer.setInterval(2000);
 connect(&m_connTimer,SIGNAL(timeout()),this,SLOT(refreshConnections()));
 connect(&m_clash,SIGNAL(delayResult(QString,int,bool)),this,SLOT(onDelayResult(QString,int,bool)));
 connect(&m_clash,SIGNAL(selectResult(QString,bool)),this,SLOT(onSelectResult(QString,bool)));
 connect(&m_clash,SIGNAL(connectionsResult(QVariantMap,bool)),this,SLOT(onConnectionsResult(QVariantMap,bool)));
 connect(&m_clash,SIGNAL(rulesResult(QVariantList,bool)),this,SLOT(onRulesResult(QVariantList,bool)));
 connect(&m_traffic,SIGNAL(message(QString)),this,SLOT(onTraffic(QString)));
 connect(&m_traffic,SIGNAL(closed()),this,SLOT(onTrafficClosed()));
 connect(&m_traffic,SIGNAL(diag(QString)),this,SLOT(onStreamDiag(QString)));
 m_echo.setProxy(QNetworkProxy(QNetworkProxy::HttpProxy,"127.0.0.1",17890));
 connect(&m_echo,SIGNAL(finished(QNetworkReply*)),this,SLOT(echoFinished(QNetworkReply*)));
 connect(&m_core,SIGNAL(started()),this,SLOT(coreStarted()));
 connect(&m_core,SIGNAL(finished(int,QProcess::ExitStatus)),this,SLOT(coreFinished(int,QProcess::ExitStatus)));
 connect(&m_core,SIGNAL(error(QProcess::ProcessError)),this,SLOT(coreError(QProcess::ProcessError)));
 connect(&m_core,SIGNAL(readyReadStandardOutput()),this,SLOT(discardCoreOutput()));
 connect(&m_core,SIGNAL(readyReadStandardError()),this,SLOT(discardCoreOutput()));
 connect(&m_probe,SIGNAL(finished(int,QProcess::ExitStatus)),this,SLOT(probeFinished(int,QProcess::ExitStatus)));
 connect(&m_probe,SIGNAL(error(QProcess::ProcessError)),this,SLOT(probeError(QProcess::ProcessError)));
 connect(&m_helper,SIGNAL(started()),this,SLOT(helperStarted()));
 connect(&m_helper,SIGNAL(readyReadStandardOutput()),this,SLOT(helperReadyRead()));
 connect(&m_helper,SIGNAL(finished(int,QProcess::ExitStatus)),this,SLOT(helperFinished(int,QProcess::ExitStatus)));
 connect(&m_helper,SIGNAL(error(QProcess::ProcessError)),this,SLOT(helperError(QProcess::ProcessError)));
 QDir().mkpath(data(""));
 writeVpnDiag();
 restoreSaved();
 loadTheme();
}
// writeVpnDiag records, once per launch, exactly what the app sandbox can see of
// the root wrapper and the bundled helper. The app runs uid 100 inside a BB10
// sandbox that (as measured) hides /var/tmp; whether it can see /bin/__root at
// all, and whether access(X_OK) on a world-rwx system binary succeeds from the
// sandbox, decides if VPN mode can work. Written to data/ so it can be read back.
void ProxyController::writeVpnDiag() const {
 QFile f(data("vpndiag.log"));
 if(!f.open(QIODevice::WriteOnly|QIODevice::Text))return;
 QFileInfo rw(QString::fromLatin1(kRootWrapper));
 QFileInfo th(asset("bin/tun-helper"));
 QString s;
 s+="uid="+QString::number((int)getuid())+" euid="+QString::number((int)geteuid())+"\n";
 s+="cwd="+QDir::current().absolutePath()+"\n";
 s+="root_wrapper="+rw.absoluteFilePath()+" exists="+QString::number(rw.exists())+" isFile="+QString::number(rw.isFile())+" isExec="+QString::number(rw.isExecutable())+" isReadable="+QString::number(rw.isReadable())+"\n";
 s+="tun_helper="+th.absoluteFilePath()+" exists="+QString::number(th.exists())+" isFile="+QString::number(th.isFile())+" isExec="+QString::number(th.isExecutable())+"\n";
 s+="vpnAvailable="+QString::number(vpnAvailable())+"\n";
 f.write(s.toUtf8());f.close();
}
ProxyController::~ProxyController() {
 if (probing()) {m_probe.terminate();m_probe.waitForFinished(1000);}
 if (running()) {m_core.terminate();m_core.waitForFinished(3000);}
 if (m_helper.state()!=QProcess::NotRunning) {m_helper.closeWriteChannel();m_helper.terminate();m_helper.waitForFinished(3000);}
 QFile::remove(data("active.yaml"));
}
// vpnAvailable gates the toggle. The bundled helper lives under the app dir and
// must be executable. The root wrapper is a world-rwx system binary we only ever
// exec by absolute path via QProcess; a BB10 sandbox can refuse access(X_OK) on
// it while still allowing the exec, so we require only that it exist, not that
// isExecutable() succeed. If even exists() is false the sandbox hides /bin and
// VPN mode is genuinely unavailable (vpndiag.log records which case holds).
bool ProxyController::vpnAvailable() const { return QFileInfo(QString::fromLatin1(kRootWrapper)).exists() && QFileInfo(asset("bin/tun-helper")).isExecutable(); }
void ProxyController::logVpn(const QString &msg) const {
 QFile f(data("vpnevents.log"));
 if(!f.open(QIODevice::WriteOnly|QIODevice::Append|QIODevice::Text))return;
 f.write((msg+"\n").toUtf8());f.close();
}
void ProxyController::setVpnMode(bool on) {
 logVpn(QString("setVpnMode(on=%1) running=%2 helper=%3 vpnAvail=%4 cur=%5")
        .arg(on).arg(running()).arg(m_helper.state()!=QProcess::NotRunning).arg(vpnAvailable()).arg(m_vpnMode));
 if(running()||m_helper.state()!=QProcess::NotRunning){logVpn(" -> reject: busy");return;}
 if(on&&!vpnAvailable()){m_vpnMode=false;setStatus(QString::fromUtf8("未检测到 root 环境，无法启用 VPN 模式"));logVpn(" -> reject: unavailable");emit changed();return;}
 if(m_vpnMode!=on){m_vpnMode=on;setStatus(on?QString::fromUtf8("VPN 模式：全局流量经 TUN 走所选节点"):QString::fromUtf8("代理模式：本机 127.0.0.1:17890"));logVpn(QString(" -> set m_vpnMode=%1").arg(m_vpnMode));}
 else logVpn(" -> no-op (already)");
 emit changed();
}
// setSplitMode toggles CN-direct split routing vs global (all via node). It only
// takes effect at the next core launch (the config is generated then), so it is
// ignored while the core/helper is running, like setVpnMode.
void ProxyController::setSplitMode(bool on){
 if(running()||m_helper.state()!=QProcess::NotRunning)return;
 if(m_splitMode!=on){m_splitMode=on;setStatus(on?QString::fromUtf8("分流模式：国内直连，国外经节点"):QString::fromUtf8("全局模式：所有流量经所选节点"));}
 emit changed();
}
// ensureGeodata copies the bundled geo databases into the core's working dir
// (data/proxy), where mihomo looks for them. Split routing's GEOSITE/GEOIP rules
// need geosite.dat + geoip.metadb; without them the core would fail to load the
// config. Copy is skipped when a same-size file is already in place.
void ProxyController::ensureGeodata() const {
 static const char *const files[2]={"geoip.metadb","geosite.dat"};
 for(int i=0;i<2;i++){
  QString srcp=asset(QString("assets/")+files[i]);
  QString dstp=data(files[i]);
  QFileInfo si(srcp);
  if(!si.exists())continue;
  QFileInfo di(dstp);
  if(di.exists()&&di.size()==si.size())continue;
  QFile::remove(dstp);
  QFile::copy(srcp,dstp);
 }
}
QString ProxyController::asset(const QString &p) const {return QDir::current().absoluteFilePath("app/native/"+p);}
QString ProxyController::data(const QString &p) const {return QDir::current().absoluteFilePath("data/proxy/"+p);}
QString ProxyController::nodeName() const {return hasNodes()?m_nodes.at(m_index):QString::fromUtf8("未选择节点");}
QString ProxyController::nodePosition() const {return hasNodes()?QString("%1 / %2").arg(m_index+1).arg(m_nodes.size()):QString();}
void ProxyController::setStatus(const QString &s) {m_status=s;emit changed();}
bool ProxyController::tool(const QStringList &args,QByteArray *out) {
 QProcess p; p.setProcessChannelMode(QProcess::SeparateChannels);p.start(asset("bin/configtool"),args);
 if(!p.waitForStarted(3000)||!p.waitForFinished(15000)||p.exitStatus()!=QProcess::NormalExit||p.exitCode()!=0) return false;
 if(out) *out=p.readAllStandardOutput();return true;
}
bool ProxyController::readNodes(const QString &path,QStringList *nodes){
 QByteArray output;QStringList args;args<<"list"<<path;
 if(!tool(args,&output)||output.size()>256*1024)return false;
 bb::data::JsonDataAccess json;QVariantList list=json.loadFromBuffer(output).toList();
 if(json.hasError()||list.isEmpty()||list.size()>1000)return false;
 QStringList parsed;
 for(int i=0;i<list.size();++i){QVariantMap n=list.at(i).toMap();if(n.value("index").toInt()!=i+1)return false;parsed<<n.value("name").toString();}
 *nodes=parsed;return true;
}
void ProxyController::restoreSaved(){
 QStringList nodes;if(!QFileInfo(data("source.yaml")).isFile())return;
 if(!readNodes(data("source.yaml"),&nodes)){m_status=QString::fromUtf8("已保存配置不可用，请重新导入");return;}
 m_nodes=nodes;m_index=0;m_delayMs.clear();m_sortByLatency=false;rebuildDisplayOrder();QFile index(data("selected.txt"));
 if(index.open(QIODevice::ReadOnly)){bool ok=false;int selected=QString::fromLatin1(index.read(20)).trimmed().toInt(&ok);if(ok&&selected>=0&&selected<m_nodes.size())m_index=selected;}
 m_status=QString::fromUtf8("已恢复配置和节点；核心未启动");
}
void ProxyController::saveIndex(){
 QFile f(data("selected.txt"));if(f.open(QIODevice::WriteOnly|QIODevice::Truncate)){f.setPermissions(QFile::ReadOwner|QFile::WriteOwner);f.write(QByteArray::number(m_index));}
}
void ProxyController::importFile(const QString &path) {
 if(running()){setStatus(QString::fromUtf8("请先停止核心"));return;}
 QFileInfo info(path);if(!info.isFile()||info.size()<1||info.size()>4*1024*1024){setStatus(QString::fromUtf8("文件不存在或超过 4 MB"));return;}
 QDir().mkpath(data(""));const QString temp=data("import.yaml");QFile::remove(temp);
 if(!QFile::copy(path,temp)||!QFile::setPermissions(temp,QFile::ReadOwner|QFile::WriteOwner)){QFile::remove(temp);setStatus(QString::fromUtf8("无法导入文件"));return;}
 QStringList nodes;
 if(!readNodes(temp,&nodes)){QFile::remove(temp);setStatus(QString::fromUtf8("配置无效或没有支持的节点（SS/VMess/VLESS/Trojan）"));return;}
 QFile::remove(data("source.yaml"));if(!QFile::rename(temp,data("source.yaml"))){QFile::remove(temp);setStatus(QString::fromUtf8("无法保存配置"));return;}
 m_nodes=nodes;m_index=0;m_delays.clear();m_delayMs.clear();m_sortByLatency=false;rebuildDisplayOrder();saveIndex();emit nodesChanged();setStatus(QString::fromUtf8("配置已导入（SS/SSR/VMess/VLESS/Trojan/Snell、TUIC/Hysteria2）"));
}
void ProxyController::previous(){if(!hasNodes())return;m_index=(m_index+m_nodes.size()-1)%m_nodes.size();saveIndex();if(apiReady())m_clash.select("BB10",m_nodes.at(m_index));emit nodesChanged();emit changed();}
void ProxyController::next(){if(!hasNodes())return;m_index=(m_index+1)%m_nodes.size();saveIndex();if(apiReady())m_clash.select("BB10",m_nodes.at(m_index));emit nodesChanged();emit changed();}
// nodeList feeds the node grid in display order (source order, or latency-sorted).
// Each map carries: src (source index, what selectNode/startup use), name, latency
// text, ms (>=0 ok / -1 fail / -2 untested, drives the colour), current (selected).
QVariantList ProxyController::nodeList() const {
 QVariantList out;
 for(int k=0;k<m_displayOrder.size();++k){
  int i=m_displayOrder.at(k);
  if(i<0||i>=m_nodes.size())continue;
  const QString name=m_nodes.at(i);
  int ms=m_delayMs.contains(name)?m_delayMs.value(name):-2;
  QString text=(ms>=0)?(QString::number(ms)+QString::fromUtf8(" ms"))
               :(ms==-1?QString::fromUtf8("超时"):QString::fromUtf8("未测"));
  QVariantMap m;
  m.insert("src",i);
  m.insert("name",name);
  m.insert("latency",text);
  m.insert("ms",ms);
  m.insert("current",i==m_index);
  out.append(m);
 }
 return out;
}
// selectNode takes a SOURCE index (the row's "src"), so sorting the display never
// breaks the source-order contract that startup (prepare, node N) relies on.
void ProxyController::selectNode(int index){
 if(!hasNodes()||index<0||index>=m_nodes.size())return;
 if(index!=m_index){m_index=index;saveIndex();}
 if(apiReady())m_clash.select("BB10",m_nodes.at(m_index));
 // Only changed() here, not nodesChanged(): the QML list moves the highlight in
 // place (two rows) so a tap never rebuilds the whole model and jumps to the top.
 emit changed();
}
// coreState is a derived, always-correct one-line run state, kept separate from the
// m_status "last action" hint so core/VPN state and action messages stop clobbering
// each other in one label.
QString ProxyController::coreState() const {
 if(!active())return QString::fromUtf8("核心未启动");
 if(m_helper.state()!=QProcess::NotRunning||m_vpnMode)
  return m_vpnUp?QString::fromUtf8("VPN 已连接（全局经所选节点）"):QString::fromUtf8("VPN 启动中…");
 return running()?QString::fromUtf8("代理运行中（127.0.0.1:17890）"):QString::fromUtf8("核心未启动");
}
void ProxyController::toggle(){ if(active())stop(); else start(); }
// rebuildDisplayOrder recomputes the node grid order. Source order by default; when
// sorting, tested nodes ascend by ms, then failed, then untested, src breaking ties.
void ProxyController::rebuildDisplayOrder(){
 m_displayOrder.clear();
 for(int i=0;i<m_nodes.size();++i)m_displayOrder.append(i);
 if(!m_sortByLatency)return;
 QList<NodeKey> keys;
 for(int i=0;i<m_nodes.size();++i){
  const QString name=m_nodes.at(i);
  int ms=m_delayMs.contains(name)?m_delayMs.value(name):-2;
  NodeKey k; k.src=i;
  if(ms>=0){k.tier=0;k.ms=ms;} else if(ms==-1){k.tier=1;k.ms=0;} else {k.tier=2;k.ms=0;}
  keys.append(k);
 }
 std::stable_sort(keys.begin(),keys.end(),NodeKeyLess());
 m_displayOrder.clear();
 for(int i=0;i<keys.size();++i)m_displayOrder.append(keys.at(i).src);
}
void ProxyController::setSortByLatency(bool on){
 if(m_sortByLatency==on)return;
 m_sortByLatency=on; rebuildDisplayOrder();
 emit nodesChanged(); emit changed();
}
QString ProxyController::nodeLatency() const { if(!hasNodes())return QString(); return m_delays.value(m_nodes.at(m_index)); }
// pickControllerPort returns the first free loopback port for the mihomo control
// API, scanning a small fixed window. A busy result just means the API won't be
// reachable; the core still runs.
int ProxyController::pickControllerPort(){
 for(int p=17891;p<=17899;++p){QTcpServer s;if(s.listen(QHostAddress::LocalHost,p)){s.close();return p;}}
 return 17891;
}
// The control-API REST calls (/proxies/<name>/delay, PUT /proxies/BB10, /rules,
// /connections) now live in ClashRest (m_clash); the controller calls m_clash.delay
// /select and receives results via onDelayResult/onSelectResult below.

// queryIps shows our egress (public) IP as each site's server would see it. The
// trick: routing is decided by destination category, so an IP-echo on a CN domain
// travels baidu's route (DIRECT in split mode -> the phone's real carrier IP) and
// an echo on a foreign domain travels google/claude's route (-> the node's exit
// IP). Both fetches go through the mixed-port proxy (m_echo's proxy) so the split
// rules apply in VPN and proxy mode alike. Two fetches cover all three sites:
// "direct" = ipip.net (CN), "node" = ipify (foreign).
void ProxyController::queryIps(){
 if(!running()){setStatus(QString::fromUtf8("请先启动核心"));return;}
 m_ips.clear();
 m_ips.insert("direct",QString::fromUtf8("查询中…"));
 m_ips.insert("node",QString::fromUtf8("查询中…"));
 // Plain HTTP on purpose: the app's Qt SSL stack uses BB10's stale system CA store
 // and cannot verify modern certs, so https echoes would fail verification. The
 // echoed value is just our own public IP, and http/80 is known to work through
 // the nodes. The app -> proxy hop is loopback.
 QNetworkReply *rn=m_echo.get(QNetworkRequest(QUrl("http://api.ipify.org")));
 rn->setProperty("route","node");
 QNetworkReply *rd=m_echo.get(QNetworkRequest(QUrl("http://myip.ipip.net")));
 rd->setProperty("route","direct");
 buildIpReport();
 setStatus(QString::fromUtf8("正在查询出口 IP"));
 emit changed();
}
void ProxyController::echoFinished(QNetworkReply *reply){
 const QString route=reply->property("route").toString();
 QString ip;
 if(reply->error()==QNetworkReply::NoError){
  QByteArray body=reply->readAll();
  QRegExp re("(\\d{1,3}\\.\\d{1,3}\\.\\d{1,3}\\.\\d{1,3})");
  if(re.indexIn(QString::fromUtf8(body))>=0)ip=re.cap(1);
  else ip=QString::fromUtf8("无法解析");
 } else ip=QString::fromUtf8("查询失败");
 m_ips.insert(route,ip);
 buildIpReport();
 bool pending=false;
 for(QMap<QString,QString>::const_iterator it=m_ips.constBegin();it!=m_ips.constEnd();++it)
  if(it.value()==QString::fromUtf8("查询中…"))pending=true;
 if(!pending)setStatus(QString::fromUtf8("出口 IP 查询完成"));
 emit changed();
 reply->deleteLater();
}
void ProxyController::buildIpReport(){
 // In split mode baidu goes direct; in global mode everything goes via the node.
 QString baiduIp=m_splitMode?m_ips.value("direct",QString::fromUtf8("—")):m_ips.value("node",QString::fromUtf8("—"));
 QString baiduTag=m_splitMode?QString::fromUtf8("直连"):QString::fromUtf8("节点");
 QString nodeIp=m_ips.value("node",QString::fromUtf8("—"));
 // google and claude share the node's egress (same foreign route), so there are
 // only two actual probes: one direct (baidu) and one node. Report them as two
 // lines backed by one result each — not three lines that reuse the single node
 // probe, which made one node success/failure show up as two.
 m_ipReport=QString::fromUtf8("baidu（%1）→ %2\ngoogle / claude（节点，同出口）→ %3")
            .arg(baiduTag).arg(baiduIp).arg(nodeIp);
}
void ProxyController::testNode(){ if(!apiReady()||!hasNodes()){setStatus(QString::fromUtf8("请先启动核心"));return;} setStatus(QString::fromUtf8("正在测速当前节点"));m_clash.delay(m_nodes.at(m_index)); emit changed(); }
void ProxyController::testAll(){
 if(!apiReady()||!hasNodes()){setStatus(QString::fromUtf8("请先启动核心"));return;}
 if(testing())return;
 m_testQueue=m_nodes;m_testOk=0;m_testBad=0;m_testTotal=m_nodes.size();
 setStatus(QString::fromUtf8("正在全部测速 0/")+QString::number(m_testQueue.size()));
 emit changed();pumpTestQueue();
}
// pumpTestQueue issues the next queued delay test; one in flight at a time keeps
// it gentle on the phone and the node.
void ProxyController::pumpTestQueue(){
 if(m_testInFlight)return;
 if(m_testQueue.isEmpty()){
  if(m_testOk||m_testBad){setStatus(QString::fromUtf8("测速完成：")+QString::number(m_testOk)+QString::fromUtf8(" 通 / ")+QString::number(m_testBad)+QString::fromUtf8(" 不可用"));if(m_sortByLatency){rebuildDisplayOrder();emit nodesChanged();}} // batch done: rows already updated in place per result; only re-sort here if sorting is on
  emit changed();return;
 }
 if(!apiReady()){m_testQueue.clear();setStatus(QString::fromUtf8("核心已停止，测速中断"));emit changed();return;}
 QString name=m_testQueue.takeFirst();
 m_testInFlight=true;m_clash.delay(name);
}
// onDelayResult threads one latency test's outcome (from ClashRest) into the test-
// queue state — tally, per-node text, progress status — then pumps the next queued
// test. m_testInFlight is still true here when a testAll run issued the test, so a
// single testNode shows "当前节点：…" while a batch run shows progress, unchanged.
void ProxyController::onDelayResult(const QString &name,int ms,bool ok){
 QString text=ok?(QString::number(ms)+QString::fromUtf8(" ms")):QString::fromUtf8("超时/失败");
 if(ok)m_testOk++;else m_testBad++;
 m_delays.insert(name,text);
 m_delayMs.insert(name,ok?ms:-1);   // numeric latency for the colour badge and sort
 logVpn(QString("delay result node=%1 ms=%2 ok=%3").arg(name).arg(ms).arg(ok));
 // Push this one node's latency to its card in place (card text/ms match nodeList()),
 // so each testAll result shows the instant it lands — no whole-list rebuild, no
 // scroll jump. The list is only fully rebuilt when the order must change (sorting).
 int src=m_nodes.indexOf(name);
 if(src>=0){
  QString cardText=ok?(QString::number(ms)+QString::fromUtf8(" ms")):QString::fromUtf8("超时");
  emit nodeLatencyUpdated(src,cardText,ok?ms:-1);
 }
 if(!m_testQueue.isEmpty()||m_testInFlight){
  int total=m_testOk+m_testBad+m_testQueue.size();
  setStatus(QString::fromUtf8("正在全部测速 ")+QString::number(m_testOk+m_testBad)+"/"+QString::number(total));
 } else {
  setStatus(QString::fromUtf8("当前节点：")+text);
  if(m_sortByLatency){rebuildDisplayOrder();emit nodesChanged();} // single test done: re-sort only if sorting; else the card is already updated in place
 }
 m_testInFlight=false;emit changed();pumpTestQueue();
}
// onSelectResult: a live node switch finished; surface only failures (success is
// already reflected by the updated nodeName/latency).
void ProxyController::onSelectResult(const QString &,bool ok){
 if(!ok)setStatus(QString::fromUtf8("切换节点失败"));
 emit changed();
}
void ProxyController::start(){
 logVpn(QString("start() m_vpnMode=%1 running=%2 hasNodes=%3").arg(m_vpnMode).arg(running()).arg(hasNodes()));
 if(running()||!hasNodes()){setStatus(QString::fromUtf8("请先选择节点"));return;}
 if(m_vpnMode)startVpn();else startProxy();
}
bool ProxyController::startProxy(){
 QTcpServer portCheck;if(!portCheck.listen(QHostAddress::LocalHost,17890)){setStatus(QString::fromUtf8("本机端口 17890 已被占用"));return false;}portCheck.close();
 QFile::remove(data("active.yaml"));QFile::remove(data("active.yaml.new"));
 m_ctrlPort=pickControllerPort();m_ctrlSecret=QUuid::createUuid().toString().remove('{').remove('}').remove('-');m_delays.clear();m_clash.setEndpoint(m_ctrlPort,m_ctrlSecret);
 ensureGeodata();
 bool split=m_splitMode&&QFileInfo(data("geosite.dat")).exists()&&QFileInfo(data("geoip.metadb")).exists();
 QStringList args;args<<"prepare"<<data("source.yaml")<<QString::number(m_index+1)<<data("active.yaml")<<QString::number(m_ctrlPort)<<m_ctrlSecret<<(split?"split":"global");
 if(!tool(args,0)){setStatus(QString::fromUtf8("无法生成安全配置"));return false;}
 QStringList env=QProcess::systemEnvironment();
 for(int i=env.size()-1;i>=0;--i)if(env.at(i).startsWith("CLASH_")||env.at(i).startsWith("SSL_CERT_FILE=")||env.at(i).startsWith("BB10_TUN_EXPERIMENT="))env.removeAt(i);
 env<<"SSL_CERT_FILE="+asset("assets/cacert.pem");m_core.setEnvironment(env);
 m_core.setProcessChannelMode(QProcess::SeparateChannels);m_stopRequested=false;
 QStringList cmd;cmd<<"-d"<<data("")<<"-f"<<data("active.yaml");m_core.start(asset("bin/mihomo"),cmd);
 setStatus(QString::fromUtf8("正在启动核心"));return true;
}
// startVpn launches the AutoRoot wrapper; once it is running (helperStarted)
// we pipe it the command that execs the root helper, which hands us the tun
// device (OWNED). The core starts then, and routes go up after it has had time
// to open the device (sendUp).
bool ProxyController::startVpn(){
 if(!vpnAvailable()){setStatus(QString::fromUtf8("未检测到 root 环境，无法启动 VPN"));return false;}
 if(m_helper.state()!=QProcess::NotRunning){setStatus(QString::fromUtf8("VPN 助手已在运行"));return false;}
 QFile::remove(data("active.yaml"));QFile::remove(data("active.yaml.new"));
 m_ctrlPort=pickControllerPort();m_ctrlSecret=QUuid::createUuid().toString().remove('{').remove('}').remove('-');m_delays.clear();m_clash.setEndpoint(m_ctrlPort,m_ctrlSecret);
 ensureGeodata();
 bool split=m_splitMode&&QFileInfo(data("geosite.dat")).exists()&&QFileInfo(data("geoip.metadb")).exists();
 QStringList args;args<<"prepare-vpn"<<data("source.yaml")<<QString::number(m_index+1)<<data("active.yaml")<<QString::number(m_ctrlPort)<<m_ctrlSecret<<(split?"split":"global");
 if(!tool(args,0)){setStatus(QString::fromUtf8("无法生成 VPN 配置"));return false;}
 m_stopRequested=false;m_vpnUp=false;m_helperBuf.clear();
 QStringList env;
 env<<"PATH=/proc/boot:/bin:/usr/bin:/base/bin:/base/usr/bin"
    <<"LD_LIBRARY_PATH=/base/usr/lib:/base/lib:/base/lib/dll:/radio/lib:/radio/lib/dll:/radio/usr/lib:/proc/boot"
    <<"LANG=C"<<"TERM=dumb";
 m_helper.setEnvironment(env);
 m_helper.setProcessChannelMode(QProcess::SeparateChannels);
 m_helper.start(QString::fromLatin1(kRootWrapper),QStringList(),QIODevice::ReadWrite);
 setStatus(QString::fromUtf8("正在获取 root 环境"));return true;
}
void ProxyController::helperStarted(){
 // Pipe the root ksh a single command that replaces it with our helper,
 // handing it this app's uid. The write channel stays open: when it closes
 // (stop, core death, or app exit) the helper sees EOF and rolls everything
 // back. The helper path and uid are the only inputs, both fixed here.
 QByteArray cmd="exec "+asset("bin/tun-helper").toUtf8()+" vpnroot "+QByteArray::number((int)getuid())+"\n";
 m_helper.write(cmd);
}
void ProxyController::helperReadyRead(){
 m_helperBuf+=m_helper.readAllStandardOutput();
 int nl;
 while((nl=m_helperBuf.indexOf('\n'))>=0){
  QString line=QString::fromUtf8(m_helperBuf.left(nl)).trimmed();m_helperBuf.remove(0,nl+1);
  if(line=="OWNED"){
   // Device handed to us: start the core (opens tun3 as our uid), raise routes after.
   launchVpnCore();
   setStatus(QString::fromUtf8("正在启动核心（VPN）"));
   QTimer::singleShot(3000,this,SLOT(sendUp()));
  } else if(line.startsWith("READY")){
   m_vpnUp=true;setStatus(QString::fromUtf8("VPN 已连接：全局流量经所选节点"));
   startNetWatch();emit changed();
  } else if(line.startsWith("ERR")){
   setStatus(QString::fromUtf8("VPN 助手报错，正在回退"));stopVpn();
  }
 }
}
void ProxyController::sendUp(){
 if(m_helper.state()==QProcess::NotRunning)return;
 if(!running()){setStatus(QString::fromUtf8("核心未启动，取消 VPN"));stopVpn();return;}
 m_helper.write("UP\n");
}
// launchVpnCore starts mihomo against the prepared VPN config. Used both on the
// initial OWNED handoff and when restarting the core after a network switch, so
// it must not depend on anything set up only once (it reuses active.yaml).
void ProxyController::launchVpnCore(){
 QStringList env=QProcess::systemEnvironment();
 for(int i=env.size()-1;i>=0;--i)if(env.at(i).startsWith("CLASH_")||env.at(i).startsWith("SSL_CERT_FILE=")||env.at(i).startsWith("BB10_TUN_EXPERIMENT="))env.removeAt(i);
 env<<"SSL_CERT_FILE="+asset("assets/cacert.pem")<<"BB10_TUN_EXPERIMENT=1";m_core.setEnvironment(env);
 // Capture the core's output to a private on-device log so node-dial failures
 // (DNS, timeout, refused) are diagnosable. Stays in the app's private dir.
 m_core.setProcessChannelMode(QProcess::MergedChannels);
 m_core.setStandardOutputFile(data("core.log")); // truncates each launch
 QStringList cmd;cmd<<"-d"<<data("")<<"-f"<<data("active.yaml");m_core.start(asset("bin/mihomo"),cmd);
}
// netSignature reads the public networking PPS object (world-readable, visible
// from the app sandbox) and returns the default interface, gateway and
// nameservers as one string. When this changes while VPN is up — a Wi-Fi<->
// cellular switch — mihomo's outbound to the node is stranded on the old
// interface, so the core is restarted on the new default route.
QString ProxyController::netSignature() const {
 QFile f("/pps/services/networking/status_public");
 if(!f.open(QIODevice::ReadOnly|QIODevice::Text))return QString();
 QString sig;
 while(!f.atEnd()){
  QByteArray line=f.readLine();
  if(line.startsWith("default_interface:")||line.startsWith("default_gateway:")||line.startsWith("nameservers:"))
   sig+=QString::fromUtf8(line);
 }
 f.close();
 return sig;
}
void ProxyController::startNetWatch(){ m_netSig=netSignature(); m_restartingCore=false; m_netWatch.start(); }
void ProxyController::stopNetWatch(){ m_netWatch.stop(); m_restartingCore=false; }
void ProxyController::onNetWatch(){
 // Only act while a VPN session is genuinely up and not being torn down.
 if(!m_vpnUp||m_stopRequested||m_helper.state()==QProcess::NotRunning||!running()||m_restartingCore)return;
 QString s=netSignature();
 if(s.isEmpty()){return;}            // transient read failure: wait for next tick
 if(m_netSig.isEmpty()){m_netSig=s;return;}
 if(s==m_netSig)return;
 logVpn(QString("net switch: restart core\n  old=%1  new=%2").arg(m_netSig).arg(s));
 m_netSig=s;
 // Restart only the core; the helper keeps tun3 and the routes (reconcile fixes
 // the DNS routes). coreFinished sees m_restartingCore and relaunches instead of
 // tearing the helper down. active.yaml is NOT removed on this path.
 m_restartingCore=true;
 setStatus(QString::fromUtf8("网络已切换，正在在新网络上重连节点"));
 m_core.terminate();
}
void ProxyController::helperFinished(int,QProcess::ExitStatus){
 // The root helper exited: it has rolled routes back and restored the device.
 stopNetWatch();
 m_helper.readAllStandardError();m_vpnUp=false;
 if(running()){m_stopRequested=true;m_core.terminate();}
 setStatus(m_stopRequested?QString::fromUtf8("VPN 已停止，网络已恢复"):QString::fromUtf8("VPN 已退出，网络已恢复"));
 emit changed();
}
void ProxyController::helperError(QProcess::ProcessError e){
 if(e==QProcess::FailedToStart){stopNetWatch();if(running()){m_stopRequested=true;m_core.terminate();}setStatus(QString::fromUtf8("无法获取 root 环境"));emit changed();}
}
void ProxyController::stopVpn(){
 stopNetWatch();
 if(running()){m_stopRequested=true;m_core.terminate();}
 // Closing the write channel gives the helper EOF, which triggers rollback;
 // terminate is a backstop.
 if(m_helper.state()!=QProcess::NotRunning){m_helper.closeWriteChannel();m_helper.terminate();}
}
void ProxyController::stop(){
 if(probing())m_probe.terminate();
 if(m_helper.state()!=QProcess::NotRunning||(m_vpnMode&&running())){m_stopRequested=true;setStatus(QString::fromUtf8("正在停止 VPN"));stopVpn();return;}
 if(running()){m_stopRequested=true;m_core.terminate();setStatus(QString::fromUtf8("正在停止核心"));}
}
void ProxyController::probe(){
 if(!running()||probing())return;
 QStringList env=QProcess::systemEnvironment();for(int i=env.size()-1;i>=0;--i)if(env.at(i).startsWith("SSL_CERT_FILE="))env.removeAt(i);
 env<<"SSL_CERT_FILE="+asset("assets/cacert.pem");m_probe.setEnvironment(env);m_probe.start(asset("bin/configtool"),QStringList()<<"probe");
 setStatus(QString::fromUtf8("正在测试 HTTPS 连接"));
}
void ProxyController::probeFinished(int code,QProcess::ExitStatus status){m_probe.readAllStandardOutput();m_probe.readAllStandardError();if(!running())return;setStatus(code==0&&status==QProcess::NormalExit?QString::fromUtf8("HTTPS 连接测试通过"):QString::fromUtf8("连接测试失败：请检查节点、网络及证书"));}
void ProxyController::probeError(QProcess::ProcessError e){if(e==QProcess::FailedToStart)setStatus(QString::fromUtf8("无法启动连接测试"));}
void ProxyController::coreStarted(){
 setStatus(QString::fromUtf8("核心进程已启动，连接尚未验证"));
 // Open the live /traffic stream shortly after launch, once the control API has
 // had a moment to bind. If the API is not up yet the connect fails and
 // onTrafficClosed reschedules, so this converges whether proxy or VPN core.
 QTimer::singleShot(1500,this,SLOT(openTraffic()));
}
void ProxyController::openTraffic(){
 if(!apiReady()){return;}
 if(m_traffic.isActive())return; // defensive: never tear down a live stream to re-open it
 m_traffic.open(m_ctrlPort,m_ctrlSecret,"/traffic");
}
// fmtSpeed renders a bytes/second rate compactly (B/s, KB/s, MB/s).
static QString fmtSpeed(qint64 bps){
 double v=(double)bps; const char *u="B/s";
 if(v>=1024.0*1024.0){v/=1024.0*1024.0;u="MB/s";}
 else if(v>=1024.0){v/=1024.0;u="KB/s";}
 int dec=(u[0]!='B'&&v<100.0)?1:0;
 return QString::number(v,'f',dec)+" "+QString::fromLatin1(u);
}
// fmtBytes renders a cumulative byte count compactly (B/KB/MB/GB), no "/s".
static QString fmtBytes(qint64 n){
 double v=(double)n; const char *u="B";
 if(v>=1024.0*1024.0*1024.0){v/=1024.0*1024.0*1024.0;u="GB";}
 else if(v>=1024.0*1024.0){v/=1024.0*1024.0;u="MB";}
 else if(v>=1024.0){v/=1024.0;u="KB";}
 int dec=(u[0]!='B'&&v<100.0)?1:0;
 return QString::number(v,'f',dec)+" "+QString::fromLatin1(u);
}
void ProxyController::onTraffic(const QString &json){
 bb::data::JsonDataAccess j;QVariantMap m=j.loadFromBuffer(json.toUtf8()).toMap();
 if(j.hasError())return;
 qint64 up=m.value("up").toLongLong();
 qint64 down=m.value("down").toLongLong();
 // Download first (what people watch), then upload; the "实时" tag keeps the row
 // legible even at 0 B/s so it never reads as "nothing here".
 m_trafficText=QString::fromUtf8("实时  ↓ ")+fmtSpeed(down)+QString::fromUtf8("   ↑ ")+fmtSpeed(up);
 emit changed();
}
void ProxyController::onStreamDiag(const QString &msg){ logVpn(QString("WS: ")+msg); }
// setConnPolling is driven by the 连接 tab's visibility: start the 2s poll (with an
// immediate first pull) while that tab is on screen, stop it otherwise so the core is
// not queried when nobody is looking.
void ProxyController::setConnPolling(bool on){
 if(on){ refreshConnections(); m_connTimer.start(); }
 else { m_connTimer.stop(); }
}
// refreshConnections pulls one /connections snapshot when the control API is up; when
// the core is down it clears the stale list once (so a stopped core shows no rows).
void ProxyController::refreshConnections(){
 if(apiReady()){ m_clash.connections(); return; }
 if(m_connCount||!m_connList.isEmpty()){
  m_connList.clear(); m_connCount=0; m_connTotalText.clear(); emit connectionsChanged();
 }
}
// onConnectionsResult turns one snapshot into display rows. Each row shows the target
// host (or ip:port), the outbound chain's first hop (the node/DIRECT actually used)
// with the matched rule, and the connection's cumulative ↓/↑. Rows are download-desc.
void ProxyController::onConnectionsResult(const QVariantMap &snap, bool ok){
 if(!ok)return; // keep the last good snapshot across a transient error
 QVariantList conns=snap.value("connections").toList();
 std::vector<ConnRow> rows; rows.reserve(conns.size());
 for(int i=0;i<conns.size();++i){
  QVariantMap c=conns.at(i).toMap();
  QVariantMap meta=c.value("metadata").toMap();
  QString host=meta.value("host").toString();
  if(host.isEmpty()){
   QString ip=meta.value("destinationIP").toString();
   QString port=meta.value("destinationPort").toString();
   host=ip.isEmpty()?QString::fromUtf8("(未知)"):(port.isEmpty()?ip:(ip+":"+port));
  }
  QVariantList chains=c.value("chains").toList();
  QString via=chains.isEmpty()?QString::fromUtf8("直连"):chains.at(0).toString();
  QString rule=c.value("rule").toString();
  QString payload=c.value("rulePayload").toString();
  QString ruleText=rule.isEmpty()?QString():(payload.isEmpty()?rule:(rule+" · "+payload));
  QString net=meta.value("network").toString().toUpper();
  QString sub=via;
  if(!ruleText.isEmpty())sub+=QString::fromUtf8("   ◂ ")+ruleText;
  qint64 dl=c.value("download").toLongLong();
  qint64 ul=c.value("upload").toLongLong();
  QVariantMap r;
  r.insert("host",host);
  r.insert("sub",sub);
  r.insert("net",net);
  r.insert("down",QString::fromUtf8("↓ ")+fmtBytes(dl));
  r.insert("up",QString::fromUtf8("↑ ")+fmtBytes(ul));
  ConnRow cr; cr.dl=dl; cr.map=r; rows.push_back(cr);
 }
 std::stable_sort(rows.begin(),rows.end(),ConnRowMore());
 m_connList.clear();
 for(size_t k=0;k<rows.size();++k)m_connList.append(rows[k].map);
 m_connCount=(int)rows.size();
 qint64 dt=snap.value("downloadTotal").toLongLong();
 qint64 ut=snap.value("uploadTotal").toLongLong();
 m_connTotalText=QString::fromUtf8("累计  ↓ ")+fmtBytes(dt)+QString::fromUtf8("   ↑ ")+fmtBytes(ut);
 emit connectionsChanged();
}
// --- UI theme -------------------------------------------------------------
// applyVisualStyle pushes m_darkTheme to the Cascades visual style, which recolours
// every stock control (Page/TitleBar backgrounds, default Labels, Buttons, Dividers)
// at once; the custom-coloured spots (node cards, accents) bind to the darkTheme
// property in QML and flip alongside it. Runtime switching is supported on 10.3.
void ProxyController::applyVisualStyle(){
 bb::cascades::Application *app=bb::cascades::Application::instance();
 if(!app||!app->themeSupport())return;
 app->themeSupport()->setVisualStyle(m_darkTheme?bb::cascades::VisualStyle::Dark
                                                :bb::cascades::VisualStyle::Bright);
}
void ProxyController::setDarkTheme(bool on){
 if(on==m_darkTheme)return;
 m_darkTheme=on;
 applyVisualStyle();
 QFile f(data("theme.txt")); // persist for next launch (0=light, 1=dark)
 if(f.open(QIODevice::WriteOnly|QIODevice::Truncate)){
  f.setPermissions(QFile::ReadOwner|QFile::WriteOwner);
  f.write(m_darkTheme?"1":"0");
 }
 emit themeChanged();
}
// loadTheme reads the persisted choice at startup and applies it before the scene is
// built, so the app opens already in the saved theme (no flash of the other one).
void ProxyController::loadTheme(){
 QFile f(data("theme.txt"));
 if(f.open(QIODevice::ReadOnly)) m_darkTheme=(QString::fromLatin1(f.read(4)).trimmed()=="1");
 applyVisualStyle();
}
// refreshRules pulls the current routing-rule set (static while the core runs, so one
// pull per 规则-tab open is enough); clears it once when the core is down.
void ProxyController::refreshRules(){
 if(apiReady()){ m_clash.rules(); return; }
 if(m_ruleCount||!m_ruleList.isEmpty()){ m_ruleList.clear(); m_ruleCount=0; emit rulesChanged(); }
}
// onRulesResult turns the /rules set into display rows {type,payload,proxy}.
void ProxyController::onRulesResult(const QVariantList &rules, bool ok){
 if(!ok)return;
 m_ruleList.clear();
 for(int i=0;i<rules.size();++i){
  QVariantMap r=rules.at(i).toMap();
  QVariantMap row;
  row.insert("type",r.value("type").toString());
  row.insert("payload",r.value("payload").toString());
  row.insert("proxy",r.value("proxy").toString());
  m_ruleList.append(row);
 }
 m_ruleCount=m_ruleList.size();
 emit rulesChanged();
}
void ProxyController::onTrafficClosed(){
 m_trafficText.clear();
 // Reconnect while a core with a live control API is still running (covers both the
 // not-yet-bound race at launch and a transient drop); stops once the core exits.
 if(running()&&m_ctrlPort>0&&!m_stopRequested)QTimer::singleShot(2000,this,SLOT(openTraffic()));
 emit changed();
}
void ProxyController::coreFinished(int,QProcess::ExitStatus){
 // Network-switch restart: the core was stopped on purpose to reconnect over the
 // new default route. Keep the helper, routes and active.yaml; relaunch the core.
 if(m_restartingCore){
  m_restartingCore=false;
  if(m_helper.state()!=QProcess::NotRunning&&!m_stopRequested){
   logVpn(QString::fromUtf8("relaunch core after net switch"));
   launchVpnCore();
   setStatus(QString::fromUtf8("网络已切换，正在重连节点"));
   return;
  }
  // helper gone or a stop came in meanwhile: fall through to normal teardown.
 }
 QFile::remove(data("active.yaml"));
 // In VPN mode a dead core must take the routes down with it: closing the
 // helper's stdin makes the root helper roll everything back on EOF.
 bool vpn=(m_helper.state()!=QProcess::NotRunning);
 if(vpn){m_helper.closeWriteChannel();m_helper.terminate();}
 stopNetWatch();
 m_ctrlPort=0;m_clash.clear();m_traffic.close();m_trafficText.clear();m_testQueue.clear();m_testInFlight=false; // control API gone with the core
 if(m_stopRequested)setStatus(vpn?QString::fromUtf8("正在恢复网络"):QString::fromUtf8("核心已停止"));
 else setStatus(vpn?QString::fromUtf8("核心已退出，正在恢复网络"):QString::fromUtf8("核心已退出，请检查节点或端口"));
}
void ProxyController::coreError(QProcess::ProcessError e){if(e==QProcess::FailedToStart){m_restartingCore=false;stopNetWatch();m_ctrlPort=0;m_clash.clear();m_traffic.close();m_trafficText.clear();m_testQueue.clear();m_testInFlight=false;QFile::remove(data("active.yaml"));if(m_helper.state()!=QProcess::NotRunning){m_helper.closeWriteChannel();m_helper.terminate();}setStatus(QString::fromUtf8("无法启动核心"));}}
void ProxyController::discardCoreOutput(){m_core.readAllStandardOutput();m_core.readAllStandardError();}
