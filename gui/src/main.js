import "@/plugins/buefy";
import "@/plugins/axios";
import "@/plugins/backendPort";
import { applyAppearanceSettings } from "@/assets/js/appearance";
applyAppearanceSettings();
import "@/plugins/mount";
import "@/plugins/dayjs";
import "@/plugins/virtual-scroll";
import Vue from "vue";
import "normalize.css";
// import "@/registerServiceWorker";
import "pace-js";
import "pace-js/themes/blue/pace-theme-corner-indicator.css";

Vue.config.productionTip = false;
