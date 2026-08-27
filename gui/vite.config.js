import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue2";
import vueJsx from "@vitejs/plugin-vue2-jsx";
import path from "path";

export default defineConfig(({ mode }) => ({
  plugins: [
    vue(),
    vueJsx({
      include: [/\.[jt]sx$/, /\.js$/],
    }),
  ],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "src"),
      vue$: "vue/dist/vue.esm.js",
    },
    extensions: [".mjs", ".js", ".ts", ".jsx", ".tsx", ".json", ".vue"],
  },
  define: {
    apiRoot: '`${localStorage["backendAddress"]}/api`',
  },
  server: {
    port: 8081,
  },
  build: {
    outDir: process.env.OUTPUT_DIR || "../web",
    sourcemap: false,
    assetsDir: "static",
    emptyOutDir: true,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (!id.includes("node_modules")) return undefined;
          if (
            id.includes("/vue/") ||
            id.includes("/vuex/") ||
            id.includes("/vue-i18n/") ||
            id.includes("/vue-router/")
          ) {
            return "vue-vendor";
          }
          if (
            id.includes("/buefy/") ||
            id.includes("/vue-virtual-scroller/")
          ) {
            return "ui-vendor";
          }
          if (id.includes("highlight.js")) return "highlight-vendor";
          if (id.includes("qrcode")) return "qrcode-vendor";
          return "vendor";
        },
      },
    },
  },
  base: process.env.publicPath || (mode === "production" ? "./" : "/"),
}));
