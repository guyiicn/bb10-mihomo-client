import bb.cascades 1.4
import bb.cascades.pickers 1.0

// Four tabs. "控制" = core session + modes + diagnostics + light/dark theme, with the
// run state and last-action hint on separate lines. "节点" = a 2-column grid of node
// cards with colour-coded latency, on-page test progress, tap-to-switch and latency
// sorting. "连接" = live /connections snapshots. "规则" = the routing-rule set.
// Custom-coloured spots bind to proxy.darkTheme so they flip with the theme; stock
// controls are recoloured by the Cascades visual style the controller sets.
TabbedPane {
    id: tabs
    showTabsOnActionBar: true
    // Poll /connections only while that tab is on screen: start when it becomes
    // active, stop when leaving, so the core isn't polled unseen. Pull /rules once
    // per 规则-tab open (the set is static while the core runs).
    onActiveTabChanged: {
        proxy.setConnPolling(activeTab === connTabTab);
        if (activeTab === ruleTabTab) {
            proxy.refreshRules();
        }
    }

    // ---- Tab 1: core control -------------------------------------------------
    Tab {
        title: "控制"
        Page {
            titleBar: TitleBar { title: "BB10 代理核心（实验版）" }
            ScrollView {
                scrollViewProperties.scrollMode: ScrollMode.Vertical
                Container {
                    leftPadding: 24; rightPadding: 24; topPadding: 24; bottomPadding: 32

                    // --- core state (stable) + last-action hint (transient) ---
                    Label { text: proxy.coreState; textFormat: TextFormat.Plain; textStyle.fontWeight: FontWeight.Bold }
                    Label {
                        text: proxy.trafficText
                        visible: proxy.running && proxy.trafficText.length > 0
                        textFormat: TextFormat.Plain
                        textStyle.fontWeight: FontWeight.Bold
                    }
                    Label { text: proxy.hasNodes ? ("当前节点：" + proxy.nodeName + (proxy.nodeLatency.length > 0 ? "  [" + proxy.nodeLatency + "]" : "")) : "未导入节点"; multiline: true; textFormat: TextFormat.Plain }
                    Label { text: proxy.status; visible: proxy.status.length > 0; multiline: true; textFormat: TextFormat.Plain; textStyle.fontSize: FontSize.Small; textStyle.color: proxy.darkTheme ? Color.create(0.62, 0.64, 0.68) : Color.Gray }
                    Button {
                        text: proxy.active ? "停止" : "启动核心"
                        enabled: proxy.active || proxy.hasNodes
                        horizontalAlignment: HorizontalAlignment.Fill
                        onClicked: proxy.toggle()
                    }

                    Divider {}

                    // --- modes (changeable only while stopped) ---
                    Container {
                        layout: StackLayout { orientation: LayoutOrientation.LeftToRight }
                        Label { text: "VPN 模式（全局）"; verticalAlignment: VerticalAlignment.Center; layoutProperties: StackLayoutProperties { spaceQuota: 1 } }
                        ToggleButton {
                            id: vpnToggle
                            checked: proxy.vpnMode
                            enabled: !proxy.active
                            onCheckedChanged: {
                                if (checked !== proxy.vpnMode) {
                                    proxy.setVpnMode(checked);
                                    checked = proxy.vpnMode;
                                }
                            }
                        }
                    }
                    Label { text: proxy.vpnAvailable ? "全机应用经 TUN 走所选节点（需 root）；关闭或退出自动恢复网络。" : "未检测到 root 环境，仅可用本机代理模式（127.0.0.1:17890）。"; multiline: true; textFormat: TextFormat.Plain; textStyle.fontSize: FontSize.Small }
                    Container {
                        layout: StackLayout { orientation: LayoutOrientation.LeftToRight }
                        Label { text: "分流（国内直连）"; verticalAlignment: VerticalAlignment.Center; layoutProperties: StackLayoutProperties { spaceQuota: 1 } }
                        ToggleButton {
                            id: splitToggle
                            checked: proxy.splitMode
                            enabled: !proxy.active
                            onCheckedChanged: {
                                if (checked !== proxy.splitMode) {
                                    proxy.setSplitMode(checked);
                                    checked = proxy.splitMode;
                                }
                            }
                        }
                    }
                    Label { text: proxy.splitMode ? "中国大陆域名/IP 直连，其余经节点（内置 GeoIP/GeoSite）。" : "所有流量经所选节点。"; multiline: true; textFormat: TextFormat.Plain; textStyle.fontSize: FontSize.Small }

                    Divider {}

                    // --- diagnostics (only while running) ---
                    Container {
                        layout: StackLayout { orientation: LayoutOrientation.LeftToRight }
                        // NOTE: an inline-object property value (layoutProperties: X {...})
                        // must NOT be followed by "; nextProp" on the same line — QML's
                        // parser rejects the ';' after the '}'. Keep these on separate lines.
                        Button {
                            text: "测试 HTTPS"
                            enabled: proxy.running && !proxy.probing
                            layoutProperties: StackLayoutProperties { spaceQuota: 1 }
                            onClicked: proxy.probe()
                        }
                        Button {
                            text: "查询出口 IP"
                            enabled: proxy.running
                            layoutProperties: StackLayoutProperties { spaceQuota: 1 }
                            onClicked: proxy.queryIps()
                        }
                    }
                    Label { text: proxy.ipReport; visible: proxy.ipReport.length > 0; multiline: true; textFormat: TextFormat.Plain; textStyle.fontSize: FontSize.Small }

                    Divider {}

                    // --- config ---
                    Button { text: "导入 YAML 配置"; enabled: !proxy.active; horizontalAlignment: HorizontalAlignment.Fill; onClicked: configPicker.open() }
                    Label { text: "支持 SS / SSR / VMess / VLESS / Trojan / Snell，及 TUIC / Hysteria2（QUIC，经测速验证可用性）。暂不含 WireGuard 与需插件的节点。"; multiline: true; textFormat: TextFormat.Plain; textStyle.fontSize: FontSize.Small; textStyle.color: proxy.darkTheme ? Color.create(0.62, 0.64, 0.68) : Color.Gray }

                    Divider {}

                    // --- appearance: light / dark theme ---
                    Container {
                        layout: StackLayout { orientation: LayoutOrientation.LeftToRight }
                        Label { text: "深色主题"; verticalAlignment: VerticalAlignment.Center; layoutProperties: StackLayoutProperties { spaceQuota: 1 } }
                        ToggleButton {
                            id: themeToggle
                            checked: proxy.darkTheme
                            onCheckedChanged: {
                                if (checked !== proxy.darkTheme) {
                                    proxy.setDarkTheme(checked);
                                }
                            }
                        }
                    }
                    Label { text: "切换浅色 / 深色界面，选择会被记住。"; multiline: true; textFormat: TextFormat.Plain; textStyle.fontSize: FontSize.Small; textStyle.color: proxy.darkTheme ? Color.create(0.62, 0.64, 0.68) : Color.Gray }
                }
            }
        }
    }

    // ---- Tab 2: node grid ----------------------------------------------------
    Tab {
        title: "节点"
        Page {
            titleBar: TitleBar { title: "节点列表" }
            Container {
                id: nodeTab
                // Refill the whole model. Called on creation and on nodesChanged
                // (import / finished test / sort) — never on the per-second traffic
                // tick. A tap moves the highlight in place (onTriggered) instead.
                function rebuildNodes() {
                    nodeModel.clear();
                    var list = proxy.nodeList();
                    for (var i = 0; i < list.length; ++i) {
                        nodeModel.append(list[i]);
                    }
                }
                // Update one node's latency cell in place (no full rebuild / scroll
                // jump), so each testAll result appears the instant it lands.
                function updateNodeLatency(src, latency, ms) {
                    for (var j = 0; j < nodeModel.size(); ++j) {
                        var it = nodeModel.value(j);
                        if (it.src === src) {
                            it.latency = latency;
                            it.ms = ms;
                            nodeModel.replace(j, it);
                            return;
                        }
                    }
                }
                onCreationCompleted: {
                    rebuildNodes();
                    proxy.nodesChanged.connect(rebuildNodes);
                    proxy.nodeLatencyUpdated.connect(updateNodeLatency);
                }

                // header: count / live progress + 全部测速
                Container {
                    leftPadding: 24; rightPadding: 24; topPadding: 16; bottomPadding: 4
                    layout: StackLayout { orientation: LayoutOrientation.LeftToRight }
                    Label {
                        text: proxy.testing ? ("测速中 " + proxy.testDone + " / " + proxy.testTotal) : ("共 " + proxy.nodeCount + " 节点")
                        verticalAlignment: VerticalAlignment.Center
                        textFormat: TextFormat.Plain
                        layoutProperties: StackLayoutProperties { spaceQuota: 1 }
                    }
                    Button { text: "全部测速"; enabled: proxy.apiReady && !proxy.testing; onClicked: proxy.testAll() }
                }
                ProgressIndicator {
                    visible: proxy.testing
                    value: proxy.testTotal > 0 ? proxy.testDone / proxy.testTotal : 0
                    leftMargin: 24; rightMargin: 24
                }
                // sort toggle + hint
                Container {
                    leftPadding: 24; rightPadding: 24; topPadding: 4; bottomPadding: 8
                    layout: StackLayout { orientation: LayoutOrientation.LeftToRight }
                    Button { text: proxy.sortByLatency ? "恢复原序" : "按延迟排序"; enabled: proxy.hasNodes; onClicked: proxy.setSortByLatency(!proxy.sortByLatency) }
                    Label {
                        text: proxy.apiReady ? "点节点即时切换" : "停止时点选=下次启动用"
                        verticalAlignment: VerticalAlignment.Center
                        textFormat: TextFormat.Plain; textStyle.fontSize: FontSize.XSmall; textStyle.color: proxy.darkTheme ? Color.create(0.62, 0.64, 0.68) : Color.Gray
                        layoutProperties: StackLayoutProperties { spaceQuota: 1 }
                    }
                }

                ListView {
                    id: nodeListView
                    layoutProperties: StackLayoutProperties { spaceQuota: 1 }
                    // cellAspectRatio = cellWidth/cellHeight. Default 1 = square cells,
                    // which on a 1440 screen made each card ~720px tall (the "ugly"). ~5
                    // makes short, wide cards. Spacing opens gaps between the cards.
                    layout: GridListLayout {
                        columnCount: 2
                        cellAspectRatio: 5.0
                        horizontalCellSpacing: 12
                        verticalCellSpacing: 12
                    }
                    dataModel: ArrayDataModel { id: nodeModel }
                    listItemComponents: [
                        ListItemComponent {
                            type: ""
                            // The component root fills the cell, so its background is the
                            // whole card: light blue for the current node, light grey else.
                            Container {
                                background: ListItemData.current
                                    ? (proxy.darkTheme ? Color.create(0.12, 0.26, 0.40) : Color.create(0.80, 0.90, 1.0))
                                    : (proxy.darkTheme ? Color.create(0.16, 0.16, 0.18) : Color.create(0.95, 0.95, 0.95))
                                leftPadding: 20; rightPadding: 20; topPadding: 14; bottomPadding: 14
                                verticalAlignment: VerticalAlignment.Center
                                Container {
                                    layout: StackLayout { orientation: LayoutOrientation.LeftToRight }
                                    Label {
                                        text: ListItemData.name
                                        multiline: false
                                        verticalAlignment: VerticalAlignment.Center
                                        layoutProperties: StackLayoutProperties { spaceQuota: 1 }
                                        textFormat: TextFormat.Plain
                                        textStyle.fontWeight: ListItemData.current ? FontWeight.Bold : FontWeight.Default
                                    }
                                    Label {
                                        text: ListItemData.current ? "●" : ""
                                        verticalAlignment: VerticalAlignment.Center
                                        textFormat: TextFormat.Plain
                                        textStyle.color: proxy.darkTheme ? Color.create(0.40, 0.72, 1.0) : Color.create(0.0, 0.45, 0.85)
                                    }
                                }
                                Label {
                                    text: ListItemData.latency
                                    textFormat: TextFormat.Plain
                                    textStyle.fontSize: FontSize.Small
                                    textStyle.color: {
                                        var ms = ListItemData.ms;
                                        var d = proxy.darkTheme;
                                        if (ms === -2) return d ? Color.create(0.55, 0.57, 0.60) : Color.Gray;
                                        if (ms === -1) return d ? Color.create(1.0, 0.45, 0.45) : Color.Red;
                                        if (ms <= 200) return d ? Color.create(0.40, 0.85, 0.47) : Color.DarkGreen;
                                        if (ms <= 500) return d ? Color.create(0.95, 0.78, 0.30) : Color.DarkYellow;
                                        return d ? Color.create(1.0, 0.45, 0.45) : Color.Red;
                                    }
                                }
                            }
                        }
                    ]
                    onTriggered: {
                        var tappedSrc = nodeModel.value(indexPath[0]).src;
                        proxy.selectNode(tappedSrc);
                        // Move the highlight in place: flip "current" only on the rows
                        // whose state changed, so no full rebuild / scroll jump.
                        for (var j = 0; j < nodeModel.size(); ++j) {
                            var it = nodeModel.value(j);
                            var want = (it.src === tappedSrc);
                            if (it.current !== want) {
                                it.current = want;
                                nodeModel.replace(j, it);
                            }
                        }
                    }
                }
            }
        }
    }

    // ---- Tab 3: live connections --------------------------------------------
    Tab {
        id: connTabTab
        title: "连接"
        Page {
            titleBar: TitleBar { title: "活动连接" }
            Container {
                id: connTab
                // Rebuild the whole list on each snapshot (connectionsChanged, ~2s).
                // Unlike the node grid this has no selection/scroll state to preserve,
                // so a full refill is simplest and correct.
                function rebuildConns() {
                    connModel.clear();
                    var list = proxy.connList();
                    for (var i = 0; i < list.length; ++i) {
                        connModel.append(list[i]);
                    }
                }
                onCreationCompleted: {
                    proxy.connectionsChanged.connect(rebuildConns);
                }

                // header: cumulative totals / count + manual refresh
                Container {
                    leftPadding: 24; rightPadding: 24; topPadding: 16; bottomPadding: 8
                    layout: StackLayout { orientation: LayoutOrientation.LeftToRight }
                    Label {
                        text: proxy.connCount > 0 ? (proxy.connTotalText + "   （" + proxy.connCount + "）") : (proxy.active ? "暂无活动连接" : "核心未运行")
                        verticalAlignment: VerticalAlignment.Center
                        textFormat: TextFormat.Plain
                        textStyle.fontSize: FontSize.Small
                        layoutProperties: StackLayoutProperties { spaceQuota: 1 }
                    }
                    Button { text: "刷新"; enabled: proxy.apiReady; onClicked: proxy.refreshConnections() }
                }
                Divider {}

                // empty state (also covers core-stopped)
                Label {
                    text: proxy.active ? "打开网页或 App 后，这里会实时显示每条连接（经哪个节点、命中哪条规则、累计流量）。" : "启动核心后显示活动连接。"
                    visible: proxy.connCount === 0
                    leftMargin: 24; rightMargin: 24; topMargin: 8
                    multiline: true
                    textFormat: TextFormat.Plain
                    textStyle.fontSize: FontSize.Small
                    textStyle.color: proxy.darkTheme ? Color.create(0.62, 0.64, 0.68) : Color.Gray
                }

                ListView {
                    id: connListView
                    layoutProperties: StackLayoutProperties { spaceQuota: 1 }
                    dataModel: ArrayDataModel { id: connModel }
                    listItemComponents: [
                        ListItemComponent {
                            type: ""
                            Container {
                                leftPadding: 24; rightPadding: 24; topPadding: 12; bottomPadding: 12
                                // line 1: host (bold) + network type
                                Container {
                                    layout: StackLayout { orientation: LayoutOrientation.LeftToRight }
                                    Label {
                                        text: ListItemData.host
                                        multiline: false
                                        verticalAlignment: VerticalAlignment.Center
                                        layoutProperties: StackLayoutProperties { spaceQuota: 1 }
                                        textFormat: TextFormat.Plain
                                        textStyle.fontWeight: FontWeight.Bold
                                    }
                                    Label {
                                        text: ListItemData.net
                                        verticalAlignment: VerticalAlignment.Center
                                        textFormat: TextFormat.Plain
                                        textStyle.fontSize: FontSize.XSmall
                                        textStyle.color: proxy.darkTheme ? Color.create(0.62, 0.64, 0.68) : Color.Gray
                                    }
                                }
                                // line 2: outbound node ◂ matched rule
                                Label {
                                    text: ListItemData.sub
                                    multiline: false
                                    textFormat: TextFormat.Plain
                                    textStyle.fontSize: FontSize.XSmall
                                    textStyle.color: proxy.darkTheme ? Color.create(0.40, 0.72, 1.0) : Color.create(0.0, 0.45, 0.85)
                                }
                                // line 3: cumulative down / up
                                Container {
                                    layout: StackLayout { orientation: LayoutOrientation.LeftToRight }
                                    Label {
                                        text: ListItemData.down
                                        textFormat: TextFormat.Plain
                                        textStyle.fontSize: FontSize.XSmall
                                        layoutProperties: StackLayoutProperties { spaceQuota: 1 }
                                    }
                                    Label {
                                        text: ListItemData.up
                                        textFormat: TextFormat.Plain
                                        textStyle.fontSize: FontSize.XSmall
                                    }
                                }
                            }
                        }
                    ]
                }
            }
        }
    }

    // ---- Tab 4: routing rules -----------------------------------------------
    Tab {
        id: ruleTabTab
        title: "规则"
        Page {
            titleBar: TitleBar { title: "分流规则" }
            Container {
                id: ruleTab
                function rebuildRules() {
                    ruleModel.clear();
                    var l = proxy.ruleList();
                    for (var i = 0; i < l.length; ++i) {
                        ruleModel.append(l[i]);
                    }
                }
                onCreationCompleted: {
                    rebuildRules();
                    proxy.rulesChanged.connect(rebuildRules);
                }

                Container {
                    leftPadding: 24; rightPadding: 24; topPadding: 16; bottomPadding: 8
                    layout: StackLayout { orientation: LayoutOrientation.LeftToRight }
                    Label {
                        text: proxy.ruleCount > 0 ? ("共 " + proxy.ruleCount + " 条规则") : (proxy.active ? "无规则" : "核心未运行")
                        verticalAlignment: VerticalAlignment.Center
                        textFormat: TextFormat.Plain
                        textStyle.fontSize: FontSize.Small
                        layoutProperties: StackLayoutProperties { spaceQuota: 1 }
                    }
                    Button { text: "刷新"; enabled: proxy.apiReady; onClicked: proxy.refreshRules() }
                }
                Divider {}

                Label {
                    text: proxy.active ? "规则自上而下匹配，命中即走对应出口。" : "启动核心后显示分流规则。"
                    visible: proxy.ruleCount === 0
                    leftMargin: 24; rightMargin: 24; topMargin: 8
                    multiline: true
                    textFormat: TextFormat.Plain
                    textStyle.fontSize: FontSize.Small
                    textStyle.color: proxy.darkTheme ? Color.create(0.62, 0.64, 0.68) : Color.Gray
                }

                ListView {
                    id: ruleListView
                    layoutProperties: StackLayoutProperties { spaceQuota: 1 }
                    dataModel: ArrayDataModel { id: ruleModel }
                    listItemComponents: [
                        ListItemComponent {
                            type: ""
                            Container {
                                leftPadding: 24; rightPadding: 24; topPadding: 10; bottomPadding: 10
                                Container {
                                    layout: StackLayout { orientation: LayoutOrientation.LeftToRight }
                                    Label {
                                        text: ListItemData.payload.length > 0 ? ListItemData.payload : ListItemData.type
                                        multiline: false
                                        verticalAlignment: VerticalAlignment.Center
                                        layoutProperties: StackLayoutProperties { spaceQuota: 1 }
                                        textFormat: TextFormat.Plain
                                        textStyle.fontWeight: FontWeight.Bold
                                    }
                                    Label {
                                        text: "→ " + ListItemData.proxy
                                        verticalAlignment: VerticalAlignment.Center
                                        textFormat: TextFormat.Plain
                                        textStyle.fontSize: FontSize.XSmall
                                        textStyle.color: proxy.darkTheme ? Color.create(0.40, 0.72, 1.0) : Color.create(0.0, 0.45, 0.85)
                                    }
                                }
                                Label {
                                    text: ListItemData.type
                                    textFormat: TextFormat.Plain
                                    textStyle.fontSize: FontSize.XSmall
                                    textStyle.color: proxy.darkTheme ? Color.create(0.55, 0.57, 0.60) : Color.Gray
                                }
                            }
                        }
                    ]
                }
            }
        }
    }

    attachedObjects: [
        FilePicker {
            id: configPicker
            title: "选择 Mihomo YAML 配置"
            type: FileType.Other; mode: FilePickerMode.Picker
            filter: ["*.yaml", "*.yml"]
            directories: ["/accounts/1000/shared/downloads", "/accounts/1000/shared/documents"]
            onFileSelected: { if (selectedFiles.length === 1) proxy.importFile(selectedFiles[0]); }
        }
    ]
}
