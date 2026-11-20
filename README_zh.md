# v2rayA [![Docker Cloud Build Status](https://img.shields.io/docker/cloud/build/v2rayA/v2raya)](https://hub.docker.com/r/mzz2017/v2raya) [![Travis (.org)](https://img.shields.io/travis/v2rayA/v2rayA?label=travis-ci%20build)](https://travis-ci.org/v2rayA/v2rayA)

[**English**](https://github.com/v2rayA/v2rayA/blob/main/README.md)&nbsp;&nbsp;&nbsp;[**简体中文**](https://github.com/v2rayA/v2rayA/blob/main/README_zh.md)

v2rayA 是一个支持全局透明代理的 V2Ray 客户端，同时兼容 SS（含 Shadowsocks 2022）、SSR、Trojan(trojan-go)、VLESS ML-KEM 加密、Tuic、AnyTLS、ShadowTLS v3、Hysteria2、Mieru 与 [Juicity](https://github.com/juicity) 协议，并支持 Clash YAML 订阅、加密订阅与 httpupgrade 等传输。 [[SSR支持清单]](https://github.com/v2rayA/dist/shadowsocksR/blob/master/README.md#ss-encrypting-algorithm)

v2rayA 致力于提供最简单的操作，满足绝大部分需求。

得益于 Web 客户端的优势，你不仅可以将其用于本地计算机，还可以轻松地将它部署在路由器或 NAS 上。

项目地址：https://github.com/v2rayA/v2rayA


## 使用方法

v2rayA 主要提供了下述使用方法：

1. 从 APT 软件源或者 AUR 安装
2. Docker
3. 自建 [scoop bucket](https://github.com/v2rayA/v2raya-scoop) (Windows 用户)
4. 自建 [homebrew tap](https://github.com/v2rayA/homebrew-v2raya)
5. 自建 [OpenWrt 仓库](https://github.com/v2rayA/v2raya-openwrt) 和 OpenWrt 官方软件源（从 OpenWrt 22.03 版本开始提供）
6. 微软 winget: https://winstall.app/apps/v2rayA.v2rayA
7. Ubuntu Snap: https://snapcraft.io/v2raya
8. 从 GitHub releases 下载二进制与安装包

详见 [**v2rayA - Docs**](https://github.com/v2rayA/v2rayA/wiki)

### 导入订阅和自动分组

在「导入」中粘贴节点列表、Clash YAML 或 SIP008，也可以选择本地文件。导入结果会显示成功数、跳过数和原因。粘贴 Karing、Clash 或 sing-box 的订阅安装链接后，可以核对和修改订阅地址、备注及节点 DNS，再确认下载。AGE 加密订阅可在同一窗口填写私钥或口令。

在出站组的「设置」中启用「按规则自动选择成员」，选择一个或多个订阅，填写节点名称的关键词或正则表达式。预览列出将要添加和移除的节点。保存后，刷新订阅或启用、禁用节点都会同步更新组成员。没有节点匹配时，组只保留仍启用的原成员。禁用最后一个成员后，该组为空。

## 界面截图

<img src="https://i.loli.net/2020/04/19/kp2oedPiSzVwgHJ.png" border="0">


## 注意

1. 程序不会将任何用户数据保存在云端，所有用户数据存放在用户本地配置文件中。

2. **不要将本项目用于不合法用途。**

## 感谢

[hq450/fancyss](https://github.com/hq450/fancyss)

[ToutyRater/v2ray-guide](https://github.com/ToutyRater/v2ray-guide/blob/master/routing/sitedata.md)

[nadoo/glider](https://github.com/nadoo/glider)

[Loyalsoldier/v2ray-rules-dat](https://github.com/Loyalsoldier/v2ray-rules-dat)

[zfl9/ss-tproxy](https://github.com/zfl9/ss-tproxy/blob/master/ss-tproxy)

## 协议

[![License: AGPL v3-only](https://img.shields.io/badge/License-AGPL%20v3-blue.svg)](https://www.gnu.org/licenses/agpl-3.0)
