<template>
  <div id="app">
    <b-navbar ref="navs" fixed-top shadow type="is-light" class="app-navbar">
      <template slot="brand">
        <b-navbar-item href="/">
          <img src="@/assets/img/logo2.png" alt="v2rayA" class="logo no-select" />
        </b-navbar-item>
        <b-navbar-item tag="div">
          <button
            id="statusTag"
            type="button"
            class="status-pill"
            :class="[`is-${statusState}`, { 'is-busy': statusBusy }]"
            :disabled="statusBusy || statusState === 'checking'"
            :title="statusActionTitle"
            :aria-label="statusActionTitle"
            @click="handleClickStatus"
          >
            <span class="status-pill__dot" aria-hidden="true"></span>
            <span class="status-pill__label">{{ runningState.running }}</span>
            <i
              v-if="statusBusy"
              class="mdi mdi-loading mdi-spin status-pill__icon"
              aria-hidden="true"
            ></i>
            <i
              v-else-if="statusState === 'running'"
              class="mdi mdi-stop-circle-outline status-pill__icon"
              aria-hidden="true"
            ></i>
            <i
              v-else-if="statusState === 'stopped' || statusState === 'waiting'"
              class="mdi mdi-play-circle-outline status-pill__icon"
              aria-hidden="true"
            ></i>
          </button>
        </b-navbar-item>
        <b-navbar-item tag="div">
          <OutboundGroupPanel
            :outbounds="outbounds"
            :current-outbound="outboundName"
            :is-mobile="isMobile"
            @select="outboundName = $event"
            @settings="handleClickOutboundSetting(null, $event)"
            @add-outbound="handleAddOutbound"
            @changed="handleGroupChanged"
            @group-deleted="handleGroupDeleted"
          />
        </b-navbar-item>
      </template>
      <template slot="start"></template>

      <template slot="end">
        <b-dropdown position="is-bottom-left" aria-role="menu" class="toolsdropdown">
          <button slot="trigger" class="navbar-item navbar-dropdown-trigger" type="button">
            <i class="mdi mdi-tools app-nav-icon" aria-hidden="true"></i>
            <span class="no-select">{{ $t("dashboard.tools") }}</span>
            <i class="mdi mdi-menu-down" aria-hidden="true"></i>
          </button>
          <b-dropdown-item
            v-if="!noviceMode"
            aria-role="menuitem"
            class="no-select"
            @click="openNodeAction('handleClickConnections')"
          >
            <i class="mdi mdi-cloud-outline app-menu-icon" aria-hidden="true"></i>
            {{ $t("operations.connections") }}
          </b-dropdown-item>
          <b-dropdown-item
            v-if="!noviceMode"
            aria-role="menuitem"
            class="no-select"
            @click="handleClickTraffic"
          >
            <i class="mdi mdi-chart-areaspline app-menu-icon" aria-hidden="true"></i>
            {{ $t("operations.traffic") }}
          </b-dropdown-item>
          <b-dropdown-item
            v-if="!noviceMode"
            aria-role="menuitem"
            class="no-select"
            @click="openNodeAction('openDetectRule')"
          >
            <i class="mdi mdi-radar app-menu-icon" aria-hidden="true"></i>
            {{ $t("operations.detectRule") }}
          </b-dropdown-item>
          <b-dropdown-item
            aria-role="menuitem"
            class="no-select"
            @click="handleClickDiagnose"
          >
            <i class="mdi mdi-stethoscope app-menu-icon" aria-hidden="true"></i>
            {{ $t("diagnose.title") }}
          </b-dropdown-item>
          <b-dropdown-item
            aria-role="menuitem"
            class="no-select"
            @click="handleClickLogs"
          >
            <i class="mdi mdi-text-box-outline app-menu-icon" aria-hidden="true"></i>
            {{ $t("common.log") }}
          </b-dropdown-item>
        </b-dropdown>

        <b-navbar-item tag="button" type="button" class="navbar-action" @click.native="handleClickSetting">
          <i class="mdi mdi-cog-outline app-nav-icon" aria-hidden="true"></i>
          {{ $t("common.setting") }}
        </b-navbar-item>

        <b-navbar-item
          tag="button"
          type="button"
          class="navbar-action notice-nav-btn"
          :title="$t('notice.title')"
          :aria-label="$t('notice.title')"
          @click.native="handleClickNotices"
        >
          <span class="notice-icon-wrap">
            <i class="mdi mdi-bell-outline app-nav-icon" aria-hidden="true"></i>
            <span v-if="unreadNotices > 0" class="notice-badge">
              {{ unreadNotices > 99 ? "99+" : unreadNotices }}
            </span>
          </span>
          <span class="notice-nav-label">{{ $t("notice.title") }}</span>
        </b-navbar-item>

        <b-navbar-item
          tag="button"
          type="button"
          class="navbar-action"
          :title="themeSwitchLabel"
          :aria-label="themeSwitchLabel"
          @click.native="toggleTheme"
        >
          <i
            :class="themePreference === 'auto' ? 'mdi mdi-theme-light-dark' : (isDarkTheme ? 'mdi mdi-weather-sunny' : 'mdi mdi-weather-night')"
            class="app-nav-icon"
            aria-hidden="true"
          ></i>
          <span class="theme-nav-label">{{ themeSwitchLabel }}</span>
        </b-navbar-item>

        <b-dropdown position="is-bottom-left" aria-role="menu" style="margin-right: 10px" class="menudropdown">
          <button slot="trigger" class="navbar-item navbar-dropdown-trigger" type="button">
            <i class="mdi mdi-account-circle-outline app-nav-icon" aria-hidden="true"></i>
            <span class="no-select">{{ username }}</span>
            <i class="mdi mdi-menu-down" aria-hidden="true"></i>
          </button>
          <b-dropdown-item custom aria-role="menuitem" class="menu-section-header">
            {{ $t("common.loggedAs", { username }) }}
          </b-dropdown-item>
          <hr class="dropdown-divider" />
          <b-dropdown-item custom aria-role="menuitem" class="menu-section-title">
            <i class="mdi mdi-earth" aria-hidden="true"></i>
            {{ $t("dashboard.language") }}
          </b-dropdown-item>
          <b-dropdown-item
            v-for="lang of langs"
            :key="lang.code"
            aria-role="menuitem"
            class="no-select lang-menu-item"
            :class="{ 'is-active': lang.flag === currentLocale }"
            @click="handleClickLang(lang.code)"
          >
            <span class="lang-item-label">{{ lang.label }}</span>
            <i
              v-if="lang.flag === currentLocale"
              class="mdi mdi-check lang-item-check"
              aria-hidden="true"
            ></i>
          </b-dropdown-item>
          <hr class="dropdown-divider" />
          <b-dropdown-item aria-role="menuitem" class="no-select" @click="handleClickAppearance">
            <i class="mdi mdi-format-font app-menu-icon" aria-hidden="true"></i>
            {{ $t("appearance.title") }}
          </b-dropdown-item>
          <b-dropdown-item aria-role="menuitem" class="no-select" @click="handleClickAbout">
            <i class="mdi mdi-information-outline app-menu-icon" aria-hidden="true"></i>
            {{ $t("common.about") }}
          </b-dropdown-item>
          <b-dropdown-item value="logout" aria-role="menuitem" class="no-select" @click="handleClickLogout">
            <i class="mdi mdi-logout app-menu-icon" aria-hidden="true"></i>
            {{ $t("operations.logout") }}
          </b-dropdown-item>
        </b-dropdown>
      </template>
    </b-navbar>
    <node
      ref="nodeRef"
      v-model="runningState"
      :outbound="outboundName"
      :outbounds="outbounds"
      :observatory="observatory"
      @novice-mode-change="handleNoviceModeChange"
    >
      <DashboardSummary
        ref="dashboardSummary"
        :traffic="traffic"
        :running-state="runningState"
        :outbound="outboundName"
        :novice-mode="noviceMode"
        :connected-count="connectedNodeCount"
        @traffic="handleClickTraffic"
        @diagnose="handleClickDiagnose"
        @settings="handleClickSetting"
        @logs="handleClickLogs"
        @active-nodes="openNodeAction('toggleSidebar')"
      />
    </node>
    <b-modal :active.sync="showCustomPorts" has-modal-card trap-focus aria-role="dialog" aria-modal
      class="modal-custom-ports">
      <ModalCustomAddress @close="showCustomPorts = false" />
    </b-modal>
    <b-modal :active.sync="loginModalActive" :can-cancel="false" has-modal-card trap-focus aria-role="dialog" aria-modal class="modal-login modal-login-app">
      <ModalLogin :first="loginModalFirst" @close="loginModalActive = false" />
    </b-modal>
    <div id="login"></div>
  </div>
</template>

<script>
import ModalSetting from "@/components/modalSetting";
import node from "@/node";
import { Base64 } from "js-base64";
import ModalCustomAddress from "@/components/modalCustomPorts";
import ModalOutboundSetting from "@/components/modalOutboundSetting";
import OutboundGroupPanel from "@/components/outboundGroupPanel";
import { parseURL } from "@/assets/js/utils";
import { waitingConnected } from "@/assets/js/networkInspect";
import axios from "@/plugins/axios";
import ModalLog from "@/components/modalLog";
import ModalLogin from "@/components/modalLogin";
import ModalTraffic from "@/components/modalTraffic";
import ModalDiagnose from "@/components/modalDiagnose";
import ModalNotice from "@/components/modalNotice";
import ModalAbout from "@/components/modalAbout";
import ModalAppearance from "@/components/modalAppearance";
import { applyAppearanceSettings } from "@/assets/js/appearance";
import DashboardSummary from "@/components/dashboardSummary";

export default {
  components: { ModalCustomAddress, node, OutboundGroupPanel, ModalLogin, DashboardSummary },
  data() {
    return {
      ws: null,
      observatory: null,
      loginModalActive: false,
      loginModalFirst: false,
      showSidebar: true,
      statusBusy: false,
      unreadNotices: 0,
      viewportWidth: typeof window !== "undefined" ? window.innerWidth : 1280,
      currentLocale: this.$i18n ? this.$i18n.locale : (localStorage["_lang"] || "zh"),
      runningState: {
        status: "checking",
        running: this.$t("common.checkRunning"),
        networkPaused: false,
        connectedServer: null,
        outboundToServerName: {},
      },
      showCustomPorts: false,
      noviceMode: false,
      langs: [
        { code: "zh_CN", label: "中文-中国", flag: "zh" },
        { code: "en_US", label: "English-US", flag: "en" },
        { code: "fa_IR", label: "فارسی", flag: "fa" },
        { code: "ru_RU", label: "Русский", flag: "ru" },
        { code: "pt_BR", label: "Português-Brasil", flag: "pt" },
      ],
      outboundName: "proxy",
      outbounds: ["proxy"],
      outboundDropdownHover: {},
      updateOutboundDropdown: true,
      themePreference: "auto",
      systemDark: window.matchMedia("(prefers-color-scheme: dark)").matches,
      wsReconnectTimer: null,
      appDestroyed: false,
      traffic: {
        up: 0,
        down: 0,
        upTotal: 0,
        downTotal: 0,
        samples: [],
      },
    };
  },
  computed: {
    username() {
      let token = localStorage["token"];
      if (!token) {
        return this.$t("common.notLogin");
      }
      let payload = JSON.parse(Base64.decode(token.split(".")[1]));
      return payload["uname"];
    },
    isMobile() {
      return this.viewportWidth < 800;
    },
    statusState() {
      if (this.runningState.status) {
        return this.runningState.status;
      }
      if (this.runningState.running === this.$t("common.isRunning")) return "running";
      if (this.runningState.running === this.$t("common.waitingNetwork")) return "waiting";
      if (this.runningState.running === this.$t("common.notRunning")) return "stopped";
      return "checking";
    },
    statusActionTitle() {
      if (this.statusState === "running") {
        return this.$t("dashboard.clickToStop");
      }
      if (this.statusState === "stopped" || this.statusState === "waiting") {
        return this.$t("dashboard.clickToStart");
      }
      return this.runningState.running;
    },
    connectedNodeCount() {
      const list = this.runningState.connectedServer;
      if (!Array.isArray(list)) return 0;
      return list.filter((s) => (s.outbound || "proxy") === this.outboundName).length;
    },
    currentLangLabel() {
      const lang = this.langs.find((l) => l.flag === this.currentLocale);
      return lang ? lang.label : "中文-中国";
    },
    isDarkTheme() {
      if (this.themePreference === "dark") return true;
      if (this.themePreference === "light") return false;
      return this.systemDark;
    },
    themeSwitchLabel() {
      if (this.themePreference === "auto") return this.$t("common.autoTheme");
      if (this.themePreference === "dark") return this.$t("common.darkTheme");
      return this.$t("common.lightTheme");
    },
  },
  mounted() {
    console.log("app created");
    this.initTheme();
    applyAppearanceSettings();
    this._onWindowResize = () => {
      this.viewportWidth = window.innerWidth;
    };
    window.addEventListener("resize", this._onWindowResize);
    let ba = localStorage.getItem("backendAddress");
    if (ba) {
      let u = parseURL(ba);
      document.title = `v2rayA - ${u.host}:${u.port}`;
    }
    // 没有 token：先检查是否需要注册，避免触发需要认证的请求导致 401 二次弹窗
    if (!localStorage["token"]) {
      // 使用重试机制确保注册页面可靠弹出
      const checkAccount = (retries = 3) => {
        this.$axios({
          url: apiRoot + "/account",
          method: "get",
        }).then((res) => {
          if (res.data.code === "SUCCESS") {
            const hasAnyAccounts = !!(res.data.data && res.data.data.hasAnyAccounts);
            // first=true -> register flow, first=false -> login flow
            this.showLoginModal(!hasAnyAccounts);
          }
        }).catch(() => {
          if (retries > 0) {
            // 网络错误时延迟重试，避免因后端尚未就绪而错过注册页面
            setTimeout(() => checkAccount(retries - 1), 2000);
          }
        });
      };
      checkAccount();
      // 无 token 时跳过需要认证的请求，注册/登录后会自动 remount
      return;
    }
    this.$axios({
      url: apiRoot + "/version",
    }).then((res) => {
      if (res.data.code === "SUCCESS") {
        let toastConf = {
          message: this.$t(
            res.data.data.dockerMode ? "welcome.docker" : "welcome.default",
            { version: res.data.data.version }
          ),
          type: "is-dark",
          position: "is-top",
          duration: 3000,
          queue: false,
        };
        if (res.data.data.foundNew) {
          toastConf.duration = 5000;
          toastConf.message +=
            ". " +
            this.$t("welcome.newVersion", {
              version: res.data.data.remoteVersion,
            });
          toastConf.type = "is-success";
        }
        this.$buefy.toast.open(toastConf);
        localStorage["docker"] = res.data.data.dockerMode;
        localStorage["version"] = res.data.data.version;
        if (res.data.data.coreVersionValid === false) {
          this.$buefy.toast.open({
            message: this.$t("version.coreVersionMismatch", { err: res.data.data.coreVersionErr || "" }),
            type: "is-danger",
            position: "is-top",
            queue: false,
            duration: 0,
          });
        } else if (res.data.data.serviceValid === false) {
          this.$buefy.toast.open({
            message: this.$t("version.v2rayInvalid"),
            type: "is-danger",
            position: "is-top",
            queue: false,
            duration: 10000,
          });
        }
        localStorage["lite"] = res.data.data.lite;
        localStorage["loadBalanceValid"] = res.data.data.loadBalanceValid;
        localStorage["variant"] = res.data.data.variant;
        localStorage["coreVersionValid"] = res.data.data.coreVersionValid;
        localStorage["coreVersionErr"] = res.data.data.coreVersionErr || "";
      }
    });
    this.$axios({
      url: apiRoot + "/outbounds",
    }).then((res) => {
      if (res.data.code === "SUCCESS") {
        this.outbounds = this.normalizeOutbounds(res.data.data.outbounds);
      }
    }).catch(() => {
      // 静默处理认证错误（如 token 过期），避免触发 axios 401 拦截器弹出登录框
    });
    this.connectWsMessage();
    this.loadUnreadNotices();
  },
  beforeDestroy() {
    this.appDestroyed = true;
    if (this._onWindowResize) {
      window.removeEventListener("resize", this._onWindowResize);
    }
    if (this.wsReconnectTimer) {
      clearTimeout(this.wsReconnectTimer);
      this.wsReconnectTimer = null;
    }
    if (this.ws) {
      this.ws.onclose = null;
      this.ws.close();
      this.ws = null;
    }
    if (this._darkMediaQuery && this._onSystemThemeChange) {
      this._darkMediaQuery.removeEventListener('change', this._onSystemThemeChange);
    }
  },
  methods: {
    openNodeAction(method) {
      const nodeRef = this.$refs.nodeRef;
      if (nodeRef && typeof nodeRef[method] === "function") {
        nodeRef[method]();
      }
    },
    handleNoviceModeChange(novice) {
      this.noviceMode = !!novice;
      if (this.$refs.nodeRef) {
        this.$refs.nodeRef.novice = this.noviceMode;
      }
    },
    showLoginModal(first) {
      this.loginModalFirst = first;
      this.loginModalActive = true;
    },
    connectWsMessage() {
      if (this.appDestroyed) return;
      const that = this;
      let url = apiRoot;
      if (!url.trim() || url.startsWith("/")) {
        url = location.protocol + "//" + location.host + url;
      }
      let protocol = "ws";
      let u = parseURL(url);
      if (u.protocol === "https") {
        protocol = "wss";
      }
      let basePath = u.path;
      if (basePath.endsWith("/api")) {
        basePath = basePath.slice(0, -4);
      }
      url = `${protocol}://${u.host}:${u.port}${basePath}/api/message?Authorization=${encodeURIComponent(localStorage["token"])}`;
      if (this.ws) {
        this.ws.onclose = null;
        this.ws.close();
        this.ws = null;
      }
      const ws = new WebSocket(url);
      // WebSocket 重连指数退避参数
      if (typeof this._wsRetries === "undefined") {
        this._wsRetries = 0;
      }
      ws.onopen = () => {
        // console.log("ws opened");
        this._wsRetries = 0; // 连接成功后重置重试计数
        // Re-sync running state on every (re)connect.  WebSocket messages are
        // not replayed, so if the connection was broken while Tun was setting up
        // routes the frontend might miss the running_state message and stay
        // stuck on "检测中" (Checking) forever.
        this.$nextTick(() => {
          if (this.$refs.nodeRef) {
            this.$refs.nodeRef.syncLatestNodeOverview();
          }
        });
      };
      ws.onmessage = (msg) => {
        msg.data && that.handleMessage(JSON.parse(msg.data));
      };
      ws.onclose = () => {
        ws.onmessage = null;
        if (that.ws === ws) {
          that.ws = null;
        }
        if (that.appDestroyed) return;
        that.recordTraffic({ up: 0, down: 0 });
        // 指数退避重连：1s, 2s, 4s, 8s... 最大 30 秒
        const delay = Math.min(1000 * Math.pow(2, that._wsRetries), 30000);
        that._wsRetries++;
        if (that.wsReconnectTimer) clearTimeout(that.wsReconnectTimer);
        that.wsReconnectTimer = setTimeout(() => {
          that.wsReconnectTimer = null;
          if (!that.appDestroyed && that.ws === null) {
            that.connectWsMessage();
          }
        }, delay);
      };
      this.ws = ws;
    },
    handleMessage(msg) {
      if (
        msg.type === "observatory" &&
        msg.body.outboundName === this.outboundName
      ) {
        this.observatory = msg;
      }
      if (msg.type === "running_state" && msg.body) {
        if (msg.body.running === false) {
          this.recordTraffic({ up: 0, down: 0 });
          this.$refs.nodeRef && this.$refs.nodeRef.notifyStopped(!!msg.body.networkPaused);
        } else {
          this.$refs.nodeRef && this.$refs.nodeRef.notifyRunning(!!msg.body.networkPaused);
        }
      }
      if (msg.type === "traffic" && msg.body) {
        this.recordTraffic(msg.body);
      }
    },
    recordTraffic(body) {
      const safeNumber = (value, fallback = 0) => {
        const parsed = Number(value);
        return Number.isFinite(parsed) && parsed >= 0 ? parsed : fallback;
      };
      this.traffic.up = safeNumber(body.up);
      this.traffic.down = safeNumber(body.down);
      this.traffic.upTotal = safeNumber(body.upTotal, this.traffic.upTotal);
      this.traffic.downTotal = safeNumber(body.downTotal, this.traffic.downTotal);
      this.traffic.samples.push({
        up: this.traffic.up,
        down: this.traffic.down,
        time: Date.now(),
      });
      if (this.traffic.samples.length > 60) {
        this.traffic.samples.splice(0, this.traffic.samples.length - 60);
      }
    },
    handleClickDiagnose() {
      this.$buefy.modal.open({ parent: this, component: ModalDiagnose, hasModalCard: true, props: { initialOutbound: this.outboundName } });
    },
    handleClickTraffic() {
      this.$buefy.modal.open({
        parent: this,
        component: ModalTraffic,
        hasModalCard: true,
        canCancel: true,
        props: {
          traffic: this.traffic,
        },
      });
    },
    handleOutboundDropdownActiveChange(active) {
      if (active) {
        this.updateOutboundDropdown = false;
        this.updateOutboundDropdown = true;
      }
    },
    handleGroupChanged() {
      // Refresh node.vue's data after group membership change from the panel
      if (this.$refs.nodeRef && this.$refs.nodeRef.created) {
        this.$refs.nodeRef.$axios({ url: apiRoot + "/touch" }).then((res) => {
          if (res.data && res.data.code === "SUCCESS") {
            this.$refs.nodeRef.refreshTableData(res.data.data.touch, res.data.data.running);
            this.$refs.nodeRef.updateConnectView();
          }
        }).catch(() => {});
      }
    },
    handleGroupDeleted(newOutbounds) {
      this.outbounds = this.normalizeOutbounds(newOutbounds);
      // If deleted group was selected, fall back to proxy
      if (!this.outbounds.includes(this.outboundName)) {
        this.outboundName = "proxy";
      }
    },
    normalizeOutbounds(outbounds) {
      const seen = new Set();
      const normalized = [];
      if (outbounds instanceof Array) {
        for (const outbound of outbounds) {
          if (typeof outbound !== "string") {
            continue;
          }
          const name = outbound.trim();
          if (!name || seen.has(name)) {
            continue;
          }
          seen.add(name);
          normalized.push(name);
        }
      }
      if (!seen.has("proxy")) {
        normalized.unshift("proxy");
      }
      return normalized;
    },
    outboundNameDecorator(outbound) {
      if (this.runningState.outboundToServerName[outbound]) {
        if (
          typeof this.runningState.outboundToServerName[outbound] === "number"
        ) {
          return `${outbound} - ${this.$t("common.loadBalance")} (${this.runningState.outboundToServerName[outbound]
            })`;
        } else {
          return `${outbound} - ${this.runningState.outboundToServerName[outbound]}`;
        }
      }
      return outbound;
    },
    handleAddOutbound() {
      this.$buefy.dialog.prompt({
        message: this.$t("outbound.addMessage"),
        inputAttrs: {
          maxlength: 10,
        },
        trapFocus: true,
        onConfirm: (outbound) => {
          let cancel;
          waitingConnected(
            this.$axios({
              url: apiRoot + "/outbound",
              method: "post",
              data: {
                outbound,
              },
              cancelToken: new axios.CancelToken(function executor(c) {
                cancel = c;
              }),
            }).then((res) => {
              if (res.data.code === "SUCCESS") {
                this.$buefy.toast.open({
                  message: this.$t("common.success"),
                  type: "is-success",
                  duration: 2000,
                  position: "is-top",
                  queue: false,
                });
                this.outbounds = this.normalizeOutbounds(res.data.data.outbounds);
              } else {
                this.$buefy.toast.open({
                  message: res.data.message,
                  type: "is-warning",
                  duration: 5000,
                  position: "is-top",
                  queue: false,
                });
              }
            }),
            3 * 1000,
            cancel
          );
        },
      });
    },
    handleDeleteOutbound(outbound) {
      let cancel;
      waitingConnected(
        this.$axios({
          url: apiRoot + "/outbound",
          method: "delete",
          data: {
            outbound,
          },
          cancelToken: new axios.CancelToken(function executor(c) {
            cancel = c;
          }),
        }).then((res) => {
          if (res.data.code === "SUCCESS") {
            this.$buefy.toast.open({
              message: this.$t("common.success"),
              type: "is-success",
              duration: 2000,
              position: "is-top",
              queue: false,
            });
            this.outbounds = this.normalizeOutbounds(res.data.data.outbounds);
            if (this.outboundName === outbound) {
              this.outboundName = "proxy";
            }
            if (outbound in this.runningState.outboundToServerName) {
              this.runningState.connectedServer =
                this.runningState.connectedServer.filter(
                  (cs) => cs.outbound !== outbound
                );
            }
          } else {
            this.$buefy.toast.open({
              message: res.data.message,
              type: "is-warning",
              duration: 5000,
              position: "is-top",
              queue: false,
            });
          }
        }),
        3 * 1000,
        cancel
      );
    },
    handleClickOutboundSetting(event, outbound) {
      if (event) event.stopPropagation();
      const that = this;
      this.$buefy.modal.open({
        parent: this,
        component: ModalOutboundSetting,
        hasModalCard: true,
        canCancel: true,
        props: {
          outbound: outbound,
        },
        events: {
          saved() {
            if (that.$refs.nodeRef) that.$refs.nodeRef.syncLatestNodeOverview();
          },
          delete() {
            that.handleDeleteOutbound(outbound);
          },
        },
      });
    },
    handleClickLang(langCode) {
      const lang = this.langs.find((l) => l.code === langCode);
      if (lang) {
        localStorage["_lang"] = lang.flag;
        this.currentLocale = lang.flag;
        this.$i18n.locale = lang.flag;
        const labelMap = {
          running: this.$t("common.isRunning"),
          stopped: this.$t("common.notRunning"),
          waiting: this.$t("common.waitingNetwork"),
          checking: this.$t("common.checkRunning"),
        };
        this.runningState.running = labelMap[this.statusState] || this.$t("common.checkRunning");
        if (this.$refs.nodeRef && typeof this.$refs.nodeRef.refreshStatusLabel === "function") {
          this.$refs.nodeRef.refreshStatusLabel();
        }
      }
    },
    loadUnreadNotices() {
      if (!localStorage["token"]) return;
      this.$axios({ url: apiRoot + "/notices", method: "get" })
        .then((res) => {
          if (res.data && res.data.code === "SUCCESS" && res.data.data) {
            this.unreadNotices = res.data.data.unread || 0;
          }
        })
        .catch(() => {});
    },
    handleClickNotices() {
      this.$buefy.modal.open({
        parent: this,
        component: ModalNotice,
        hasModalCard: true,
        canCancel: true,
        events: {
          close: () => {
            this.loadUnreadNotices();
          },
        },
      });
      this.unreadNotices = 0;
    },
    handleOnOutboundMouseEnter(outbound) {
      this.outboundDropdownHover = { [outbound]: true };
    },
    handleOnOutboundMouseLeave() {
      this.outboundDropdownHover = {};
    },
    handleClickSetting() {
      const that = this;
      this.$buefy.modal.open({
        parent: this,
        component: ModalSetting,
        hasModalCard: true,
        canCancel: true,
        events: {
          clickPorts() {
            that.showCustomPorts = true;
          },
          "novice-change"(novice) {
            that.handleNoviceModeChange(novice);
          },
          "settings-updated"() {
            if (that.$refs.dashboardSummary) that.$refs.dashboardSummary.refreshSetting();
          },
        },
      });
    },
    handleClickAppearance() {
      this.$buefy.modal.open({
        parent: this,
        component: ModalAppearance,
        hasModalCard: true,
        canCancel: true,
      });
    },
    handleClickAbout() {
      this.$buefy.modal.open({
        parent: this,
        component: ModalAbout,
        hasModalCard: true,
        canCancel: true,
      });
    },
    handleClickStatus() {
      if (this.statusBusy) return;
      if (this.statusState === "stopped" || this.statusState === "waiting") {
        let cancel;
        this.statusBusy = true;
        waitingConnected(
          this.$axios({
            url: apiRoot + "/v2ray",
            method: "post",
            cancelToken: new axios.CancelToken(function executor(c) {
              cancel = c;
            }),
          })
            .then((res) => {
              if (res.data.code === "SUCCESS") {
                Object.assign(this.runningState, {
                  status: "running",
                  running: this.$t("common.isRunning"),
                  networkPaused: false,
                  connectedServer: res.data.data.touch.connectedServer,
                });
              } else {
                this.$buefy.toast.open({
                  message: res.data.message,
                  type: "is-warning",
                  duration: 5000,
                  position: "is-top",
                  queue: false,
                });
              }
            })
            .catch(() => {})
            .finally(() => {
              this.statusBusy = false;
            }),
          3 * 1000,
          cancel
        );
      } else if (this.statusState === "running") {
        this.statusBusy = true;
        this.$axios({
          url: apiRoot + "/v2ray",
          method: "delete",
        })
          .then((res) => {
            if (res.data.code === "SUCCESS") {
              Object.assign(this.runningState, {
                status: "stopped",
                running: this.$t("common.notRunning"),
                networkPaused: false,
                connectedServer: res.data.data.touch.connectedServer,
              });
            } else {
              this.$buefy.toast.open({
                message: res.data.message,
                type: "is-warning",
                duration: 5000,
                position: "is-top",
                queue: false,
              });
            }
          })
          .catch(() => {})
          .finally(() => {
            this.statusBusy = false;
          });
      }
    },
    handleClickLogout() {
      localStorage.removeItem("token");
      this.$remount();
    },
    initTheme() {
      const stored = localStorage.getItem('theme');
      this.themePreference = (stored === 'dark' || stored === 'light') ? stored : 'auto';
      this._darkMediaQuery = window.matchMedia('(prefers-color-scheme: dark)');
      this._onSystemThemeChange = (e) => {
        this.systemDark = e.matches;
        this.applyThemeClass();
      };
      this._darkMediaQuery.addEventListener('change', this._onSystemThemeChange);
      this.applyThemeClass();
    },
    applyThemeClass() {
      document.body.classList.toggle('theme-dark', this.isDarkTheme);
      document.documentElement.classList.toggle('theme-dark', this.isDarkTheme);
    },
    toggleTheme() {
      const order = ['auto', 'light', 'dark'];
      const idx = order.indexOf(this.themePreference);
      this.themePreference = order[(idx + 1) % order.length];
      localStorage.setItem('theme', this.themePreference);
      this.applyThemeClass();
    },
    handleClickLogs() {
      this.$buefy.modal.open({
        parent: this,
        component: ModalLog,
        hasModalCard: true,
        canCancel: true,
      });
    },
  },
};
</script>

<style lang="scss">
@import "assets/scss/reset.scss";
@import "assets/scss/dark-theme.scss";
</style>

<style lang="scss" scoped>
#app {
  margin: 0;
}

.menucontainer {
  padding: 20px;
}

.logo {
  min-height: 2.25rem;
  margin-left: 0.5rem;
  margin-right: 0.5rem;
}

.app-nav-icon {
  margin-right: 0.3rem;
  font-size: 1.2em;
}

.app-menu-icon {
  margin-right: 0.45rem;
  font-size: 1.05em;
  vertical-align: middle;
}

.status-pill {
  appearance: none;
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0.3rem 0.72rem;
  border-radius: 999px;
  border: 1px solid transparent;
  font: inherit;
  font-size: 0.82rem;
  font-weight: 600;
  white-space: nowrap;
  cursor: pointer;
  transition: background-color 0.15s ease, border-color 0.15s ease, transform 0.12s ease;

  &:disabled {
    cursor: default;
    opacity: 0.75;
  }

  &.is-running {
    background: var(--va-success-soft);
    border-color: var(--va-success);
    color: var(--va-success);

    &:hover:not(:disabled) {
      background: var(--va-success);
      color: #ffffff;
    }
  }

  &.is-stopped {
    background: var(--va-surface-subtle);
    border-color: var(--va-border-strong);
    color: var(--va-text);

    &:hover:not(:disabled) {
      background: var(--va-primary);
      border-color: var(--va-primary);
      color: #ffffff;
    }
  }

  &.is-waiting {
    background: var(--va-warning-soft);
    border-color: var(--va-warning);
    color: var(--va-warning);

    &:hover:not(:disabled) {
      background: var(--va-warning);
      color: #ffffff;
    }
  }

  &.is-checking {
    background: var(--va-surface-subtle);
    border-color: var(--va-border);
    color: var(--va-text-muted);
  }
}

.status-pill__dot {
  width: 0.48rem;
  height: 0.48rem;
  border-radius: 50%;
  background: currentColor;
  flex-shrink: 0;
}

.status-pill__icon {
  font-size: 1.05em;
  line-height: 1;
}

.notice-icon-wrap {
  position: relative;
  display: inline-flex;
  align-items: center;
}

.notice-badge {
  position: absolute;
  top: -0.35rem;
  right: -0.25rem;
  min-width: 1.05rem;
  height: 1.05rem;
  padding: 0 0.25rem;
  border-radius: 999px;
  background: var(--va-danger);
  color: #ffffff;
  font-size: 0.65rem;
  font-weight: 700;
  line-height: 1.05rem;
  text-align: center;
}

.notice-nav-label,
.theme-nav-label {
  display: none;
}

.menu-section-header {
  font-size: 0.8rem;
  color: var(--va-text-muted);
}

.menu-section-title {
  font-size: 0.72rem;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--va-text-subtle);
  padding-top: 0.25rem;
  padding-bottom: 0.25rem;
}

.lang-menu-item {
  display: flex !important;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
}

.lang-item-check {
  color: var(--va-primary);
  font-size: 1rem;
}

.navbar-action,
.navbar-dropdown-trigger {
  appearance: none;
  border: 0;
  background-color: transparent;
  color: inherit;
  cursor: pointer;
  font: inherit;
  text-align: left;
}

.navbar-action:focus-visible,
.navbar-dropdown-trigger:focus-visible,
.status-pill:focus-visible {
  outline: 2px solid var(--va-primary);
  outline-offset: -2px;
}

@media screen and (max-width: 959px) {
  .notice-nav-label,
  .theme-nav-label {
    display: inline;
  }
}
</style>

<style lang="scss">
html {
  #app {
    min-height: calc(100vh - 3.5rem);
  }
}

.app-navbar.navbar.is-light {
  background-color: var(--va-surface);
  border-bottom: 1px solid var(--va-border);
  box-shadow: var(--va-shadow-sm);
}

@media screen and (max-width: 1023px) {
  .dropdown.is-mobile-modal .dropdown-menu {
    left: 0 !important;
    right: 0 !important;
    margin: auto;
    transform: unset !important;
  }
}

@media screen and (max-width: 959px) {
  .app-navbar.navbar {
    display: block;
  }

  .app-navbar .navbar-brand {
    align-items: stretch;
    display: flex;
  }

  .app-navbar .navbar-brand > .navbar-item {
    align-items: center;
    display: flex;
  }

  .app-navbar .navbar-burger {
    display: block;
  }

  .app-navbar .navbar-menu,
  .app-navbar .navbar-start,
  .app-navbar .navbar-end {
    display: block;
  }

  .app-navbar .navbar-menu {
    display: none;
    max-height: calc(100vh - 3.5rem);
    overflow: auto;
    padding: 0.5rem 0;
    background: var(--va-surface);
    box-shadow: var(--va-shadow-md);
  }

  .app-navbar .navbar-menu.is-active {
    display: block;
  }

  .app-navbar .navbar-menu .navbar-item,
  .app-navbar .navbar-menu .navbar-link {
    align-items: center;
    display: flex;
    width: 100%;
  }

  .app-navbar .navbar-end {
    margin-left: 0;
  }

  .app-navbar .dropdown,
  .app-navbar .dropdown-trigger {
    display: block;
    width: 100%;
  }
}

.dropdown-item:focus:not(:focus-visible) {
  outline: none !important;
}

.no-select {
  user-select: none;
  -webkit-user-drag: none;
}

.menudropdown .dropdown-item {
  font-size: 0.85rem;
}

.leave-right {
  margin-right: 0.6rem;
}

.lazy {
  p {
    margin: 0.5em 0;
  }
}

a.navbar-item:focus,
a.navbar-item:focus-within,
a.navbar-item:hover,
a.navbar-item.is-active,
.navbar-link:focus,
.navbar-link:focus-within,
.navbar-link:hover,
.navbar-link.is-active,
.is-link,
a {
  color: var(--va-link);
}

.icon-loading_ico-copy {
  font-size: 2.5rem;
  color: var(--va-text-subtle);
  animation: loading-rotate 2s infinite linear;
}

.modal-custom-ports {
  z-index: 999;
}

/* Keep App-level login modal above any other modal overlays. */
.modal-login-app {
  z-index: 5000 !important;
}

.modal-login-app .modal-background {
  z-index: 0 !important;
}

.modal-login-app .modal-content,
.modal-login-app .animation-content,
.modal-login-app .modal-card {
  position: relative;
  z-index: 1 !important;
  pointer-events: auto !important;
}

.after-line-dot5 {
  p {
    margin-bottom: 0.5em;
  }
}

.about-small {
  font-size: 0.85em;
  text-indent: 1em;
  color: var(--va-text-muted);
}

.margin-right-2em {
  margin-right: 2em;
}

.justify-content-space-between {
  justify-content: space-between;
}

.padding-right-1rem {
  padding-right: 2rem !important;
}

.dropdown-menu .is-fullwidth {
  width: 100%;
}

.dropdown-menu .outbound-setting {
  position: absolute;
  right: -1.5rem;
  top: 0;
  font-size: 1rem;
}

.navbar-item .dropdown-menu .dropdown-content {
  max-height: calc(100vh - 60px);
  overflow-y: auto;
}
</style>
