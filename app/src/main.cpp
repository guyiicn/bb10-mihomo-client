#include <bb/cascades/Application>
#include <bb/cascades/QmlDocument>
#include <bb/cascades/AbstractPane>
#include <QDeclarativeContext>
#include <QFile>
#include <QDir>
#include "proxycontroller.h"

// QML runtime errors (binding failures, unknown properties, createRootObject
// problems) are emitted as qWarning/qCritical, which otherwise vanish on-device.
// Route them to data/proxy/qml.log so a launch failure is diagnosable without a
// desktop. The path is recomputed per message (cwd is stable) rather than held in
// a global QString — a global object with a constructor needs __dso_handle, which
// this BB10 link config does not provide, and that breaks the executable link.
static void qmlMsgHandler(QtMsgType type, const char *msg){
 QFile f(QDir::current().absoluteFilePath("data/proxy/qml.log"));
 if(!f.open(QIODevice::WriteOnly|QIODevice::Append|QIODevice::Text))return;
 const char *t = type==QtDebugMsg?"D":type==QtWarningMsg?"W":type==QtCriticalMsg?"C":"F";
 f.write(QByteArray(t)+": "+QByteArray(msg)+"\n");
 f.close();
}
Q_DECL_EXPORT int main(int argc,char **argv){
 bb::cascades::Application app(argc,argv);
 QDir().mkpath(QDir::current().absoluteFilePath("data/proxy"));
 QFile::remove(QDir::current().absoluteFilePath("data/proxy/qml.log"));
 qInstallMsgHandler(qmlMsgHandler);
 ProxyController controller(&app);
 bb::cascades::QmlDocument *doc=bb::cascades::QmlDocument::create("asset:///main.qml").parent(&app);
 doc->documentContext()->setContextProperty("proxy",&controller);
 if(doc->hasErrors()){qWarning("QmlDocument parse errors in main.qml");return 1;}
 bb::cascades::AbstractPane *root=doc->createRootObject<bb::cascades::AbstractPane>();
 if(!root){qWarning("createRootObject returned null");return 1;}
 app.setScene(root);
 return bb::cascades::Application::exec();
}
