"use strict";

import Vue from "vue";
import axios from "axios";
import {
  SnackbarProgrammatic,
  ToastProgrammatic,
  ModalProgrammatic,
} from "buefy";
import { parseURL } from "@/assets/js/utils";
import browser from "@/assets/js/browser";
import modalCustomPorts from "../components/modalCustomPorts";
import i18n from "../plugins/i18n";
import { nanoid } from "nanoid";

Vue.prototype.$axios = axios;

axios.defaults.timeout = 60 * 1000; // timeout: 60秒

axios.interceptors.request.use(
  (config) => {
    if (Object.prototype.hasOwnProperty.call(localStorage, "token")) {
      config.headers.Authorization = `${localStorage["token"]}`;
      config.headers["X-V2raya-Request-Id"] = nanoid();
    }
    return config;
  },
  (err) => {
    console.log("!", err.name, err.message);
    ToastProgrammatic.open({
      message: err.message,
      type: "is-warning",
      position: "is-top",
      duration: 5000,
    });
    return Promise.reject(err);
  }
);

let informed = "";

function informNotRunning(url = localStorage["backendAddress"]) {
  if (informed === url) {
    return;
  }
  informed = url;
  SnackbarProgrammatic.open({
    message: i18n.t("axios.messages.optimizeBackend"),
    type: "is-primary",
    queue: false,
    duration: 10000,
    position: "is-top",
    actionText: i18n.t("operations.yes"),
    onAction: () => {
      // this.showCustomPorts = true;
      ModalProgrammatic.open({
        component: modalCustomPorts,
        hasModalCard: true,
        customClass: "modal-custom-ports",
      });
    },
  });
  SnackbarProgrammatic.open({
    message: i18n.t("axios.messages.noBackendFound", { url }),
    type: "is-warning",
    queue: false,
    position: "is-top",
    duration: 10000,
    actionText: i18n.t("operations.helpManual"),
    onAction: () => {
      window.open(i18n.t("axios.urls.usage"), "_blank");
    },
  });
}

axios.interceptors.response.use(
  function (res) {
    return res;
  },
  function (err) {
    console.log("!!", err.name, err.message);
    console.log(Object.assign({}, err));
    if (err.code === "ECONNABORTED" && err.isAxiosError) {
      return Promise.reject(err);
    }
    let u, host;
    if (err.config) {
      u = parseURL(err.config.url);
      host = u.host;
    }
    if (err.response && err.response.status === 401) {
      const reqUrl = (err.config && err.config.url) || "";
      const isAuthAction = reqUrl.includes("/api/login") || reqUrl.includes("/api/account");
      if (isAuthAction) {
        // Let login/register request callers handle their own UI state.
        return Promise.reject(err);
      }

      if (localStorage["token"]) {
        // Centralize auth recovery in App.vue's mounted() flow to avoid
        // programmatic modal stacking and overlay conflicts.
        localStorage.removeItem("token");
        window.location.reload();
      }
      return Promise.reject(err);
    } else if (
      location.protocol.substr(0, 5) === "https" &&
      u.protocol === "http"
    ) {
      // https frontend communicating with http backend
      let msg = i18n.t("axios.messages.cannotCommunicate.0");
      if (host === "localhost" || host === "local" || host === "127.0.0.1") {
        if (browser.versions.webKit) {
          // Chrome and other WebKit browsers allow access to http://localhost, 
          // failures are likely due to backend service not being started.
          informNotRunning(u.source.replace(u.relative, ""));
          return;
        }
        if (browser.versions.gecko) {
          msg = i18n.t("axios.messages.cannotCommunicate.1");
        }
      }
      SnackbarProgrammatic.open({
        message: msg,
        type: "is-warning",
        position: "is-top",
        queue: false,
        duration: 10000,
        actionText: i18n.t("operations.switchSite"),
        onAction: () => {
          window.open("http://v.v2raya.org", "_self");
        },
      });
      SnackbarProgrammatic.open({
        message: i18n.t("axios.messages.optimizeBackend"),
        type: "is-primary",
        queue: false,
        duration: 10000,
        position: "is-top",
        actionText: i18n.t("operations.yes"),
        onAction: () => {
          // this.showCustomPorts = true;
          ModalProgrammatic.open({
            component: modalCustomPorts,
            hasModalCard: true,
            customClass: "modal-custom-ports",
          });
        },
      });
    } else if (
      (err.message && err.message === "Network Error") ||
      (err.config && err.config.url === "/api/version")
    ) {
      informNotRunning(u.source.replace(u.relative, ""));
    } else {
      // other errors
      if (
        !err.message ||
        (err.message && err.message.indexOf("404") >= 0) ||
        (err.response && err.response.status === 404)
      ) {
        // Interface doesn't exist, or expected error (e.g. cancellation), maybe legacy server version - ignore
        return Promise.reject(err);
      }
      console.log("!other");
      ToastProgrammatic.open({
        message: err,
        type: "is-warning",
        position: "is-top",
        queue: false,
        duration: 5000,
      });
    }
    return Promise.reject(err);
  }
);

export default axios;
