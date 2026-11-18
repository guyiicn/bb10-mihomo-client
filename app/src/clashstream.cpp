#include "clashstream.h"
#include <QCryptographicHash>
#include <QUuid>
#include <QtNetwork/QHostAddress>
#include <QtNetwork/QAbstractSocket>

ClashStream::ClashStream(QObject *parent) : QObject(parent), m_state(Idle), m_port(0) {
 connect(&m_sock,SIGNAL(connected()),this,SLOT(onConnected()));
 connect(&m_sock,SIGNAL(readyRead()),this,SLOT(onReadyRead()));
 connect(&m_sock,SIGNAL(disconnected()),this,SLOT(onDisconnected()));
 connect(&m_sock,SIGNAL(error(QAbstractSocket::SocketError)),this,SLOT(onSockError()));
}
void ClashStream::onSockError(){
 emit diag(QString("sockErr: ")+m_sock.errorString());
 // A refused/failed connect emits error() but not disconnected(), so drive the
 // teardown here too: reset to Idle and signal closed() so the owner reconnects.
 // (The control API binds only after the core finishes loading geodata, which can
 // take several seconds, so the first attempts are expected to be refused.)
 if(m_state!=Idle){ m_state=Idle; m_sock.abort(); emit closed(); }
}
ClashStream::~ClashStream(){ m_sock.abort(); }

void ClashStream::open(int port,const QString &secret,const QString &path){
 close();
 if(port<=0)return;
 m_port=port;m_secret=secret;m_path=path;m_rx.clear();m_accept.clear();
 m_state=Handshaking;
 emit diag(QString("open %1 :%2").arg(path).arg(port));
 m_sock.connectToHost(QHostAddress::LocalHost,port);
}
void ClashStream::close(){
 if(m_state==Open){ sendFrame(0x8,QByteArray()); }      // polite close
 bool was=(m_state!=Idle);
 m_state=Idle;m_rx.clear();
 m_sock.abort();
 if(was)emit closed();
}

void ClashStream::onConnected(){ emit diag("tcp connected -> upgrade"); if(m_state==Handshaking)sendUpgrade(); }
void ClashStream::onDisconnected(){
 emit diag(QString("tcp disconnected (state=%1)").arg((int)m_state));
 if(m_state!=Idle){ m_state=Idle; emit closed(); }
}

void ClashStream::sendUpgrade(){
 // Random 16-byte key -> base64; the server must echo base64(SHA1(key+GUID)).
 QByteArray key=QUuid::createUuid().toRfc4122().toBase64();
 m_accept=QCryptographicHash::hash(key+"258EAFA5-E914-47DA-95CA-C5AB0DC85B11",
                                   QCryptographicHash::Sha1).toBase64();
 // token in the query (what the web dashboards use) + Authorization as a fallback.
 QString sep=m_path.contains('?')?"&":"?";
 QByteArray req;
 req+="GET "+m_path.toUtf8()+sep.toUtf8()+"token="+m_secret.toUtf8()+" HTTP/1.1\r\n";
 req+="Host: 127.0.0.1:"+QByteArray::number(m_port)+"\r\n";
 req+="Upgrade: websocket\r\n";
 req+="Connection: Upgrade\r\n";
 req+="Sec-WebSocket-Key: "+key+"\r\n";
 req+="Sec-WebSocket-Version: 13\r\n";
 req+="Authorization: Bearer "+m_secret.toUtf8()+"\r\n";
 req+="\r\n";
 m_sock.write(req);
}

void ClashStream::onReadyRead(){
 m_rx+=m_sock.readAll();
 if(m_state==Handshaking){ if(!parseHandshake())return; }
 if(m_state==Open)parseFrames();
}

// parseHandshake consumes the HTTP 101 response from m_rx. Returns true once the
// header block is complete and accepted (state flips to Open); false while waiting.
bool ClashStream::parseHandshake(){
 int end=m_rx.indexOf("\r\n\r\n");
 if(end<0)return false;
 QByteArray head=m_rx.left(end);
 m_rx.remove(0,end+4);
 // Status line must be 101, and Sec-WebSocket-Accept must match our expected key.
 bool ok=head.left(12).contains("101");
 bool acceptOk=false;
 QList<QByteArray> lines=head.split('\n');
 for(int i=0;i<lines.size();++i){
  QByteArray l=lines.at(i).trimmed();
  int c=l.indexOf(':');
  if(c<0)continue;
  if(l.left(c).trimmed().toLower()=="sec-websocket-accept")
   acceptOk=(l.mid(c+1).trimmed()==m_accept);
 }
 emit diag(QString("handshake: statusOk=%1 acceptOk=%2 head1=%3")
           .arg(ok).arg(acceptOk).arg(QString::fromLatin1(head.left(head.indexOf('\n')).trimmed())));
 if(!ok||!acceptOk){ close(); return false; }
 m_state=Open;emit diag("ws OPEN");emit opened();
 return true;
}

// parseFrames decodes every complete RFC 6455 frame currently in m_rx. Server push
// frames are unmasked text; we also answer ping and honour close.
void ClashStream::parseFrames(){
 for(;;){
  if(m_rx.size()<2)return;
  const unsigned char *d=(const unsigned char*)m_rx.constData();
  quint8 opcode=d[0]&0x0F;
  bool masked=(d[1]&0x80)!=0;
  quint64 len=d[1]&0x7F;
  int off=2;
  if(len==126){ if(m_rx.size()<4)return; len=((quint64)d[2]<<8)|d[3]; off=4; }
  else if(len==127){ if(m_rx.size()<10)return; len=0; for(int i=0;i<8;++i)len=(len<<8)|d[2+i]; off=10; }
  int maskOff=off; int dataOff=off+(masked?4:0);
  if((quint64)m_rx.size()<(quint64)dataOff+len)return;         // frame incomplete
  QByteArray payload=m_rx.mid(dataOff,(int)len);
  if(masked){ const unsigned char *mk=d+maskOff; for(int i=0;i<payload.size();++i)payload[i]=payload[i]^mk[i&3]; }
  m_rx.remove(0,dataOff+(int)len);
  if(opcode==0x1){ emit message(QString::fromUtf8(payload)); }
  else if(opcode==0x9){ sendFrame(0xA,payload); }             // ping -> pong
  else if(opcode==0x8){ close(); return; }                    // close
  // 0x2 binary / 0xA pong / 0x0 continuation: ignored (mihomo sends small text frames)
 }
}

// sendFrame writes one masked client->server frame (used only for pong/close).
void ClashStream::sendFrame(quint8 opcode,const QByteArray &payload){
 if(m_sock.state()!=QAbstractSocket::ConnectedState)return;
 QByteArray f;
 f.append((char)(0x80|opcode));
 int n=payload.size();
 if(n<=125){ f.append((char)(0x80|n)); }
 else { f.append((char)(0x80|126)); f.append((char)((n>>8)&0xFF)); f.append((char)(n&0xFF)); }
 QByteArray mask=QUuid::createUuid().toRfc4122().left(4);
 f.append(mask);
 QByteArray mp=payload;
 for(int i=0;i<mp.size();++i)mp[i]=mp[i]^mask[i&3];
 f.append(mp);
 m_sock.write(f);
}
