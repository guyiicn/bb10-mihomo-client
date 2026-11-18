#pragma once
#include <QObject>
#include <QByteArray>
#include <QString>
#include <QtNetwork/QTcpSocket>

// ClashStream is a minimal WebSocket client for mihomo's streaming control-API
// endpoints (/traffic, /logs, /memory, /connections). BB10 ships Qt 4.8, which has
// no QWebSocket, so the handshake and RFC 6455 framing are hand-rolled over a
// QTcpSocket on loopback (no TLS). It only needs to RECEIVE server-push text frames
// (plus answer pings), so client->server masking is used only for the pong/close it
// sends. Auth uses the ?token=<secret> query param the web dashboards use, with an
// Authorization: Bearer header as a belt-and-braces fallback.
class ClashStream : public QObject {
 Q_OBJECT
public:
 explicit ClashStream(QObject *parent=0);
 ~ClashStream();
 // (Re)open a stream to ws://127.0.0.1:<port><path>. path like "/traffic".
 void open(int port, const QString &secret, const QString &path);
 void close();
 bool isOpen() const { return m_state==Open; }
 // isActive is true from the moment a connect starts until it is fully torn down
 // (Handshaking or Open). Callers use it to avoid re-opening a live stream, which
 // would close() it, emit closed(), and (with an auto-reconnect owner) loop.
 bool isActive() const { return m_state!=Idle; }

signals:
 void message(const QString &json);   // one decoded text frame
 void opened();
 void closed();
 void diag(const QString &msg);       // human-readable trace for on-device logging

private slots:
 void onConnected();
 void onReadyRead();
 void onDisconnected();
 void onSockError();

private:
 void sendUpgrade();
 bool parseHandshake();               // consume HTTP 101 from m_rx; true once done
 void parseFrames();                  // decode any complete frames in m_rx
 void sendFrame(quint8 opcode, const QByteArray &payload); // masked client frame

 enum State { Idle, Handshaking, Open } m_state;
 QTcpSocket m_sock;
 QByteArray m_rx;        // bytes received, consumed as handshake then frames
 QByteArray m_accept;    // expected Sec-WebSocket-Accept for this connection
 int m_port;
 QString m_secret;
 QString m_path;
};
