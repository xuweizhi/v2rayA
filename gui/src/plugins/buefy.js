import Vue from "vue";
import Buefy, { ConfigProgrammatic, SnackbarProgrammatic, ToastProgrammatic } from "buefy";
import "@/assets/scss/buefy.scss";
import "@mdi/font/css/materialdesignicons.css";

Vue.use(Buefy);
ConfigProgrammatic.setOptions({
  defaultProgrammaticPromise: true,
});

// Buefy renders string messages as HTML. Use its Vue slot for plain text so
// API messages, node names and network errors are safe at every call site.
const h = new Vue().$createElement;
function textNotices(component) {
  return {
    open(params) {
      const options = typeof params === "string" ? { message: params } : params;
      return component.open({
        ...options,
        message: [h("div", { class: "text" }, [String(options.message ?? "")])],
      });
    },
  };
}

export const toast = textNotices(ToastProgrammatic);
export const snackbar = textNotices(SnackbarProgrammatic);
Vue.prototype.$buefy.toast = toast;
Vue.prototype.$buefy.snackbar = snackbar;
