#include "clashrest.h"
#include <bb/data/JsonDataAccess>
#include <QtNetwork/QNetworkRequest>
#include <QtNetwork/QNetworkReply>
#include <QUrl>

ClashRest::ClashRest(QObject *parent) : QObject(parent), m_port(0) {
 connect(&m_nam,SIGNAL(finished(QNetworkReply*)),this,SLOT(onFinished(QNetworkReply*)));
}
void ClashRest::setEndpoint(int port,const QString &secret){ m_port=port>0?port:0; m_secret=secret; }
void ClashRest::clear(){ m_port=0; m_secret.clear(); }

QNetworkRequest ClashRest::authed(const QUrl &url) const {
 QNetworkRequest req(url);
 req.setRawHeader("Authorization",QByteArray("Bearer ")+m_secret.toUtf8());
 return req;
}

// GET /proxies/<name>/delay?timeout=5000&url=<https target>. The name and the test
// URL are each percent-encoded exactly once by QUrl/addQueryItem. (A previous
// hand-rolled version pre-encoded the url and then wrapped it in QUrl(), double-
// encoding the '%' so mihomo got a literal http%3A%2F%2F... target and returned 503
// for every node.) HTTPS/443 target so the delay reflects real TLS reachability;
// this is only correct because the core runs with SSL_CERT_FILE set (see the
// controller's launch code), otherwise its /delay client cannot verify certs.
void ClashRest::delay(const QString &name){
 if(!ready())return;
 QUrl url;
 url.setScheme("http");
 url.setHost("127.0.0.1");
 url.setPort(m_port);
 url.setPath(QString("/proxies/%1/delay").arg(name));
 url.addQueryItem("timeout","5000");
 url.addQueryItem("url","https://www.gstatic.com/generate_204");
 QNetworkReply *r=m_nam.get(authed(url));
 r->setProperty("kind","delay");r->setProperty("node",name);
}
// PUT /proxies/<group> {"name":"<node>"} — switch the select group live.
void ClashRest::select(const QString &group,const QString &name){
 if(!ready())return;
 QUrl url;
 url.setScheme("http");
 url.setHost("127.0.0.1");
 url.setPort(m_port);
 url.setPath(QString("/proxies/%1").arg(group));
 QNetworkRequest req=authed(url);
 req.setHeader(QNetworkRequest::ContentTypeHeader,QVariant(QString("application/json")));
 QString esc=name;esc.replace("\\","\\\\").replace("\"","\\\"");
 QByteArray body=QString("{\"name\":\"%1\"}").arg(esc).toUtf8();
 QNetworkReply *r=m_nam.put(req,body);
 r->setProperty("kind","select");r->setProperty("node",name);
}
void ClashRest::rules(){
 if(!ready())return;
 QUrl url;url.setScheme("http");url.setHost("127.0.0.1");url.setPort(m_port);url.setPath("/rules");
 QNetworkReply *r=m_nam.get(authed(url));r->setProperty("kind","rules");
}
void ClashRest::connections(){
 if(!ready())return;
 QUrl url;url.setScheme("http");url.setHost("127.0.0.1");url.setPort(m_port);url.setPath("/connections");
 QNetworkReply *r=m_nam.get(authed(url));r->setProperty("kind","connections");
}

void ClashRest::onFinished(QNetworkReply *reply){
 const QString kind=reply->property("kind").toString();
 const bool netOk=(reply->error()==QNetworkReply::NoError);
 QByteArray body=reply->readAll();
 if(kind=="delay"){
  const QString node=reply->property("node").toString();
  int ms=0;bool ok=false;
  if(netOk){
   // {"delay": <ms>} on success, {"message": "..."} on failure.
   bb::data::JsonDataAccess j;QVariantMap m=j.loadFromBuffer(body).toMap();
   if(!j.hasError()&&m.contains("delay")){ms=m.value("delay").toInt();ok=true;}
  }
  emit delayResult(node,ms,ok);
 } else if(kind=="select"){
  emit selectResult(reply->property("node").toString(),netOk);
 } else if(kind=="rules"){
  QVariantList list;bool ok=false;
  if(netOk){bb::data::JsonDataAccess j;QVariantMap m=j.loadFromBuffer(body).toMap();if(!j.hasError()){list=m.value("rules").toList();ok=true;}}
  emit rulesResult(list,ok);
 } else if(kind=="connections"){
  QVariantMap snap;bool ok=false;
  if(netOk){bb::data::JsonDataAccess j;QVariantMap m=j.loadFromBuffer(body).toMap();if(!j.hasError()){snap=m;ok=true;}}
  emit connectionsResult(snap,ok);
 }
 reply->deleteLater();
}
