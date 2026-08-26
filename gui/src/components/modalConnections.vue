<template>
  <div class="modal-card" style="max-width: 820px; margin: auto">
    <header class="modal-card-head">
      <p class="modal-card-title">
        {{ $t("connections.title") }}
        <span style="font-size: 0.75em; color: #888">
          ({{ filteredConnections.length }})
        </span>
      </p>
    </header>
    <section class="modal-card-body">
      <div class="conn-toolbar">
        <b-button size="is-small" @click="paused = !paused">
          {{ paused ? $t("connections.resume") : $t("connections.pause") }}
        </b-button>
        <b-button size="is-small" @click="connections = []">
          {{ $t("connections.clear") }}
        </b-button>
        <b-input
          v-model="search"
          size="is-small"
          :placeholder="$t('connections.searchPlaceholder')"
          style="max-width: 260px"
        />
        <span class="conn-note">{{ $t("connections.note") }}</span>
      </div>
      <b-table
        :data="filteredConnections"
        :per-page="200"
        :current-page.sync="page"
        hoverable
        style="max-height: 380px"
      >
        <b-table-column field="time" :label="$t('connections.time')" width="90" v-slot="p">
          {{ formatTime(p.row.time) }}
        </b-table-column>
        <b-table-column field="source" :label="$t('connections.source')" width="190" v-slot="p">
          <span class="code-font">{{ p.row.source }}</span>
        </b-table-column>
        <b-table-column field="dest" :label="$t('connections.dest')" v-slot="p">
          <span
            class="code-font dest-cell"
            :title="$t('connections.copyDest')"
            @click="copyText(p.row.dest)"
          >
            {{ p.row.dest }}
          </span>
        </b-table-column>
        <b-table-column field="outbound" :label="$t('connections.outbound')" width="150" v-slot="p">
          {{ p.row.outbound }}
        </b-table-column>
        <b-table-column :label="$t('operations.name')" width="110" v-slot="p">
          <b-button
            size="is-small"
            type="is-info"
            outlined
            :disabled="!destHost(p.row)"
            @click="openCreateRule(p.row)"
          >
            {{ $t("connections.createRule") }}
          </b-button>
        </b-table-column>
      </b-table>
    </section>
    <b-modal
      v-model="showRuleDialog"
      has-modal-card
      trap-focus
      aria-role="dialog"
      aria-modal
    >
      <div class="modal-card" style="max-width: 460px; margin: auto">
        <header class="modal-card-head">
          <p class="modal-card-title">{{ $t("connections.createRule") }}</p>
        </header>
        <section class="modal-card-body">
          <b-field :label="$t('connections.domainPattern')">
            <b-input v-model="newRule.domains" class="code-font" />
          </b-field>
          <b-field :label="$t('dns.colServer')">
            <b-select v-model="newRule.server" expanded>
              <option value="localhost">localhost</option>
              <option value="223.5.5.5">223.5.5.5</option>
              <option value="119.29.29.29">119.29.29.29</option>
              <option value="1.0.0.1">1.0.0.1</option>
              <option value="https://dns.google/dns-query">https://dns.google/dns-query</option>
              <option value="https://1.1.1.1/dns-query">https://1.1.1.1/dns-query</option>
            </b-select>
          </b-field>
          <b-field :label="$t('dns.colOutbound')">
            <b-select v-model="newRule.outbound" expanded>
              <option value="direct">direct</option>
              <option value="proxy">proxy</option>
              <option
                v-for="out in outbounds"
                :key="out"
                :value="out"
              >{{ out }}</option>
            </b-select>
          </b-field>
        </section>
        <footer class="modal-card-foot flex-end">
          <button class="button" type="button" @click="showRuleDialog = false">
            {{ $t("operations.cancel") }}
          </button>
          <button class="button is-primary" @click="createRule">
            {{ $t("operations.saveApply") }}
          </button>
        </footer>
      </div>
    </b-modal>
  </div>
</template>

<script>
import { handleResponse } from "@/assets/js/utils";
export default {
  name: "ModalConnections",
  data: () => ({
    connections: [],
    paused: false,
    page: 1,
    search: "",
    timer: null,
    outbounds: ["proxy"],
    showRuleDialog: false,
    newRule: { domains: "", server: "223.5.5.5", outbound: "direct" },
  }),
  computed: {
    filteredConnections() {
      const q = (this.search || "").toLowerCase();
      if (!q) return this.connections;
      return this.connections.filter(
        (c) =>
          (c.dest || "").toLowerCase().includes(q) ||
          (c.source || "").toLowerCase().includes(q) ||
          (c.outbound || "").toLowerCase().includes(q)
      );
    },
  },
  created() {
    this.$axios({ url: apiRoot + "/outbounds", method: "get" }).then((res) => {
      if (res.data && res.data.data && res.data.data.outbounds) {
        this.outbounds = res.data.data.outbounds;
      }
    });
    this.refresh();
    this.timer = setInterval(() => {
      if (!this.paused) this.refresh();
    }, 3000);
  },
  beforeDestroy() {
    if (this.timer) clearInterval(this.timer);
  },
  methods: {
    refresh() {
      this.$axios({
        url: apiRoot + "/connections?limit=500",
        method: "get",
      }).then((res) => {
        if (res.data && res.data.code === "SUCCESS" && res.data.data) {
          this.connections = res.data.data.connections || [];
        }
      });
    },
    formatTime(t) {
      const d = new Date(t);
      const pad = (n) => String(n).padStart(2, "0");
      return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
    },
    copyText(text) {
      if (!text) return;
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(text).then(() => {
          this.$buefy.toast.open({
            message: this.$t("common.success"),
            type: "is-primary",
            position: "is-top",
            duration: 1500,
            queue: false,
          });
        });
      }
    },
    destHost(row) {
      const dest = (row.dest || "").replace(/^(tcp|udp):/, "");
      if (/^https?:\/\//.test(dest)) {
        try {
          return new URL(dest).hostname;
        } catch (e) {
          return null;
        }
      }
      const host = dest.split(":")[0];
      if (host && !/^\d+\.\d+\.\d+\.\d+$/.test(host) && host.indexOf(":") < 0) {
        return host;
      }
      return null;
    },
    openCreateRule(row) {
      const host = this.destHost(row);
      if (!host) return;
      // Create a suffix rule like "domain:example.com" for the DNS module.
      this.newRule.domains = "domain:" + host;
      this.newRule.outbound = row.outbound === "direct" ? "direct" : "proxy";
      this.showRuleDialog = true;
    },
    createRule() {
      if (!this.newRule.domains.trim() || !this.newRule.server.trim()) {
        return;
      }
      this.$axios({ url: apiRoot + "/dnsRules", method: "get" }).then((res) => {
        let rules = [];
        if (res.data && res.data.code === "SUCCESS" && res.data.data) {
          rules = res.data.data.rules || [];
        }
        rules.push({
          server: this.newRule.server,
          domains: this.newRule.domains,
          outbound: this.newRule.outbound,
        });
        this.$axios({
          url: apiRoot + "/dnsRules",
          method: "put",
          data: rules,
        }).then((res2) => {
          handleResponse(res2, this, () => {
            this.$buefy.toast.open({
              message: this.$t("common.success"),
              type: "is-primary",
              position: "is-top",
              duration: 3000,
              queue: false,
            });
            this.showRuleDialog = false;
          });
        });
      });
    },
  },
};
</script>
