#pragma once
#include <QObject>
#include <QProcess>
#include <QStringList>
#include <QTimer>
#include <QMap>
#include <QVariantList>
#include <QtNetwork/QNetworkAccessManager>
#include "clashrest.h"
#include "clashstream.h"
class QNetworkReply;

class ProxyController : public QObject {
 Q_OBJECT
 Q_PROPERTY(QString status READ status NOTIFY changed)
 Q_PROPERTY(QString nodeName READ nodeName NOTIFY changed)
 Q_PROPERTY(QString nodePosition READ nodePosition NOTIFY changed)
 Q_PROPERTY(bool hasNodes READ hasNodes NOTIFY changed)
 Q_PROPERTY(bool running READ running NOTIFY changed)
 Q_PROPERTY(bool probing READ probing NOTIFY changed)
 Q_PROPERTY(bool vpnMode READ vpnMode WRITE setVpnMode NOTIFY changed)
 Q_PROPERTY(bool splitMode READ splitMode WRITE setSplitMode NOTIFY changed)
 Q_PROPERTY(bool vpnAvailable READ vpnAvailable NOTIFY changed)
 Q_PROPERTY(bool apiReady READ apiReady NOTIFY changed)
 Q_PROPERTY(bool testing READ testing NOTIFY changed)
 Q_PROPERTY(QString nodeLatency READ nodeLatency NOTIFY changed)
 Q_PROPERTY(QString ipReport READ ipReport NOTIFY changed)
 Q_PROPERTY(QString trafficText READ trafficText NOTIFY changed)
 Q_PROPERTY(QString coreState READ coreState NOTIFY changed)   // stable core/VPN run state
 Q_PROPERTY(bool active READ active NOTIFY changed)            // core OR vpn helper running
 Q_PROPERTY(int nodeCount READ nodeCount NOTIFY changed)
 Q_PROPERTY(int testDone READ testDone NOTIFY changed)         // finished count in a testAll run
 Q_PROPERTY(int testTotal READ testTotal NOTIFY changed)       // total for the current testAll run
 Q_PROPERTY(bool sortByLatency READ sortByLatency NOTIFY changed)
 Q_PROPERTY(int connCount READ connCount NOTIFY connectionsChanged)      // active connection count
 Q_PROPERTY(QString connTotalText READ connTotalText NOTIFY connectionsChanged) // cumulative ↓/↑ totals
 Q_PROPERTY(int ruleCount READ ruleCount NOTIFY rulesChanged)             // active routing-rule count
 Q_PROPERTY(bool darkTheme READ darkTheme WRITE setDarkTheme NOTIFY themeChanged) // light/dark UI theme
public:
 explicit ProxyController(QObject *parent=0);
 ~ProxyController();
 QString status() const { return m_status; }
 QString nodeName() const;
 QString nodePosition() const;
 bool hasNodes() const { return !m_nodes.isEmpty(); }
 bool running() const { return m_core.state()!=QProcess::NotRunning; }
 bool probing() const { return m_probe.state()!=QProcess::NotRunning; }
 bool vpnMode() const { return m_vpnMode; }
 Q_INVOKABLE void setVpnMode(bool on);
 bool splitMode() const { return m_splitMode; }
 Q_INVOKABLE void setSplitMode(bool on);
 bool vpnAvailable() const;
 bool apiReady() const { return running() && m_ctrlPort>0; }
 bool testing() const { return !m_testQueue.isEmpty() || m_testInFlight; }
 QString nodeLatency() const; // latency text for the current node, "" if none
 Q_INVOKABLE void importFile(const QString &file);
 Q_INVOKABLE void previous();
 Q_INVOKABLE void next();
 // nodeList() feeds the node ListView: one map {name, latency, current} per node,
 // in m_nodes order (so a tapped row's index maps straight back to m_index).
 Q_INVOKABLE QVariantList nodeList() const;
 // selectNode(i) is the list-tap equivalent of previous()/next(): switch the live
 // node over the control API when running, else just pick what the next start uses.
 Q_INVOKABLE void selectNode(int index);
 Q_INVOKABLE void start();
 Q_INVOKABLE void stop();
 Q_INVOKABLE void probe();
 Q_INVOKABLE void testNode();   // latency-test the current node via the control API
 Q_INVOKABLE void testAll();    // latency-test every node, sequentially
 QString ipReport() const { return m_ipReport; }
 Q_INVOKABLE void queryIps();   // show our egress IP for baidu/google/claude
 QString trafficText() const { return m_trafficText; } // live ↑/↓ speed, "" when idle
 QString coreState() const;                   // derived, always-correct run-state line
 bool active() const { return running() || m_helper.state()!=QProcess::NotRunning; }
 int nodeCount() const { return m_nodes.size(); }
 int testDone() const { return m_testOk+m_testBad; }
 int testTotal() const { return m_testTotal; }
 bool sortByLatency() const { return m_sortByLatency; }
 Q_INVOKABLE void toggle();                   // start if idle, stop if active (one button)
 Q_INVOKABLE void setSortByLatency(bool on);  // reorder the node list by latency (or restore source order)
 int connCount() const { return m_connCount; }
 QString connTotalText() const { return m_connTotalText; }
 // connList() feeds the 连接 ListView: one map {host, sub, down, up, net} per active
 // connection, sorted by cumulative download descending (biggest talkers first).
 Q_INVOKABLE QVariantList connList() const { return m_connList; }
 int ruleCount() const { return m_ruleCount; }
 // ruleList() feeds the 规则 ListView: one {type,payload,proxy} per routing rule.
 Q_INVOKABLE QVariantList ruleList() const { return m_ruleList; }
 Q_INVOKABLE void refreshRules(); // pull the current /rules set (static while running)
 bool darkTheme() const { return m_darkTheme; }
 // setDarkTheme switches the Cascades visual style (stock controls) and flips the
 // custom-colour tokens the QML binds to, then persists the choice for next launch.
 Q_INVOKABLE void setDarkTheme(bool on);
public slots:
 // refreshConnections pulls one /connections snapshot (no-op when the core is down,
 // clearing the stale list once). setConnPolling drives the 2s poll from the 连接
 // tab's visibility, so it only runs while that tab is on screen.
 void refreshConnections();
 void setConnPolling(bool on);
signals:
 void changed();
 void connectionsChanged();                   // a fresh /connections snapshot is ready
 // nodesChanged fires only when the node set, a node's latency, or the current
 // selection changes — NOT on the per-second traffic tick — so the ListView is
 // rebuilt only when it needs to be, never mid-scroll each second.
 void nodesChanged();
 // nodeLatencyUpdated fires once per finished delay test (batch or single) with the
 // node's SOURCE index and its fresh latency text/ms, so QML updates just that one
 // card in place — a 68-node testAll shows each result the instant it lands, not the
 // whole list only at batch end.
 void nodeLatencyUpdated(int src, const QString &latency, int ms);
 void rulesChanged();                   // a fresh /rules set is ready
 void themeChanged();                   // light/dark theme toggled
private slots:
 void coreStarted();
 void coreFinished(int code, QProcess::ExitStatus status);
 void coreError(QProcess::ProcessError error);
 void discardCoreOutput();
 void probeFinished(int code, QProcess::ExitStatus status);
 void probeError(QProcess::ProcessError error);
 void helperStarted();
 void helperReadyRead();
 void helperFinished(int code, QProcess::ExitStatus status);
 void helperError(QProcess::ProcessError error);
 void sendUp();
 void onNetWatch();
 void onDelayResult(const QString &name, int ms, bool ok);
 void onSelectResult(const QString &name, bool ok);
 void onTraffic(const QString &json);   // one /traffic frame {up,down}
 void onTrafficClosed();                // stream dropped: reconnect while core runs
 void onConnectionsResult(const QVariantMap &snap, bool ok); // one /connections snapshot
 void onRulesResult(const QVariantList &rules, bool ok);    // the /rules set
 void onStreamDiag(const QString &msg); // ClashStream trace -> vpnevents.log
 void openTraffic();                    // (re)open the /traffic websocket
 void echoFinished(QNetworkReply *reply);
private:
 bool tool(const QStringList &args, QByteArray *out);
 void ensureGeodata() const; // copy geoip.metadb + geosite.dat into the core's data dir
 bool readNodes(const QString &path, QStringList *nodes);
 void restoreSaved();
 void saveIndex();
 void setStatus(const QString &s);
 QString asset(const QString &path) const;
 QString data(const QString &path) const;
 void writeVpnDiag() const;
 void logVpn(const QString &msg) const;
 bool startProxy();
 bool startVpn();
 void stopVpn();
 void launchVpnCore();
 QString netSignature() const;
 void startNetWatch();
 void stopNetWatch();
 int pickControllerPort();
 void buildIpReport();
 void pumpTestQueue();
 void rebuildDisplayOrder(); // fill m_displayOrder (source order, or sorted by latency)
 QProcess m_core;
 QProcess m_probe;
 QProcess m_helper;
 QTimer m_netWatch;
 QString m_netSig;
 bool m_restartingCore;
 QStringList m_nodes;
 int m_index;
 QString m_status;
 bool m_stopRequested;
 bool m_vpnMode;
 bool m_splitMode;
 bool m_vpnUp;
 QByteArray m_helperBuf;
 ClashRest m_clash;            // control-API REST client (owns its own QNAM)
 ClashStream m_traffic;        // /traffic websocket (live ↑/↓ speed)
 QString m_trafficText;
 QNetworkAccessManager m_echo; // separate manager, routed via the mixed-port proxy
 int m_ctrlPort;
 QString m_ctrlSecret;
 QMap<QString,QString> m_delays; // node name -> latency text
 QMap<QString,QString> m_ips;    // diagnostic domain -> resolved IP text
 QString m_ipReport;             // formatted baidu/google/claude IP lines
 QStringList m_testQueue;        // nodes still to test in a testAll run
 bool m_testInFlight;            // a delay request is outstanding
 int m_testOk;
 int m_testBad;
 int m_testTotal;                // node count captured when a testAll run begins
 QMap<QString,int> m_delayMs;    // node name -> latency ms (>=0 ok, -1 fail); absent = untested
 QList<int> m_displayOrder;      // node ListView order as source indices (identity or latency-sorted)
 bool m_sortByLatency;           // node list sorted by latency vs source order
 QTimer m_connTimer;             // 2s poll driving the 连接 tab (runs only while it is visible)
 QVariantList m_connList;        // last /connections snapshot as display rows (download-desc)
 int m_connCount;                // active connection count in that snapshot
 QString m_connTotalText;        // cumulative ↓/↑ totals line for the 连接 header
 QVariantList m_ruleList;        // last /rules set as display rows ({type,payload,proxy})
 int m_ruleCount;                // rule count in that set
 bool m_darkTheme;               // UI theme (false=light/Bright, true=dark)
 void loadTheme();               // read the persisted theme choice and apply it
 void applyVisualStyle();        // push m_darkTheme to the Cascades visual style
};
