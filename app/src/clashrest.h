#pragma once
#include <QObject>
#include <QString>
#include <QVariant>
#include <QVariantList>
#include <QVariantMap>
#include <QtNetwork/QNetworkAccessManager>
class QNetworkReply;
class QNetworkRequest;
class QUrl;

// ClashRest is a thin async wrapper over mihomo's external-controller REST API
// (loopback, no TLS). It owns the endpoint (control port + bearer secret) and one
// QNetworkAccessManager, and turns each call into a signal-delivered result, so
// the controller no longer hand-rolls requests and demuxes replies inline.
//
// BB10 ships Qt 4.8: query items go on QUrl itself via addQueryItem (there is no
// Qt5 QUrlQuery), and JSON is parsed with bb::data::JsonDataAccess in the .cpp.
class ClashRest : public QObject {
 Q_OBJECT
public:
 explicit ClashRest(QObject *parent=0);
 // Point at the running core's control API. port<=0 means "no endpoint" (offline).
 void setEndpoint(int port, const QString &secret);
 void clear();                       // control API gone with the core
 bool ready() const { return m_port>0; }
 int port() const { return m_port; }

 // Latency-test one proxy over HTTPS: GET /proxies/<name>/delay -> delayResult.
 void delay(const QString &name);
 // Switch a select group live without restarting: PUT /proxies/<group> -> selectResult.
 void select(const QString &group, const QString &name);
 // Dashboard reads (stage 2 consumers): GET /rules, GET /connections.
 void rules();
 void connections();

signals:
 // ms is the measured delay on ok; undefined otherwise.
 void delayResult(const QString &name, int ms, bool ok);
 void selectResult(const QString &name, bool ok);
 void rulesResult(const QVariantList &rules, bool ok);
 void connectionsResult(const QVariantMap &snapshot, bool ok);

private slots:
 void onFinished(QNetworkReply *reply);

private:
 QNetworkRequest authed(const QUrl &url) const;
 QNetworkAccessManager m_nam;
 int m_port;
 QString m_secret;
};
