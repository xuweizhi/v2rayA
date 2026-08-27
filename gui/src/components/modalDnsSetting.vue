<template>
  <div class="modal-card dns-setting-modal">
    <header class="modal-card-head">
      <p class="modal-card-title">{{ $t("dns.title") }}</p>
      <a
        class="help-link"
        href="https://www.v2fly.org/config/dns.html"
        target="_blank"
        rel="noopener noreferrer"
        :title="$t('dns.helpTooltip')"
      >
        <b-icon icon="help-circle-outline" size="is-small" />
        {{ $t("dns.help") }}
      </a>
    </header>
    <section class="modal-card-body">
      <!-- DNS rules table -->
      <div class="dns-table">
        <!-- Header row -->
        <div class="dns-row dns-header">
          <div class="col-server">{{ $t("dns.colServer") }}</div>
          <div class="col-domains">{{ $t("dns.colDomains") }}</div>
          <div class="col-outbound">{{ $t("dns.colOutbound") }}</div>
          <div class="col-actions"></div>
        </div>

        <!-- Rule rows -->
        <div
          v-for="(rule, index) in rules"
          :key="index"
          class="dns-row dns-data-row"
        >
          <div class="col-server">
            <span class="dns-mobile-label">{{ $t("dns.colServer") }}</span>
            <b-input
              v-model="rule.server"
              size="is-small"
              :placeholder="$t('dns.serverPlaceholder')"
              class="code-font"
            />
          </div>
          <div class="col-domains">
            <span class="dns-mobile-label">{{ $t("dns.colDomains") }}</span>
            <b-input
              v-model="rule.domains"
              type="textarea"
              size="is-small"
              :placeholder="$t('dns.domainsPlaceholder')"
              class="code-font dns-domains-input"
              rows="2"
            />
          </div>
          <div class="col-outbound">
            <span class="dns-mobile-label">{{ $t("dns.colOutbound") }}</span>
            <b-select v-model="rule.outbound" size="is-small" expanded>
              <option value="direct">direct</option>
              <option
                v-for="out in outbounds"
                :key="out"
                :value="out"
              >{{ out }}</option>
            </b-select>
          </div>
          <div class="col-actions">
            <b-button
              size="is-small"
              type="is-danger"
              icon-left="delete"
              @click="removeRule(index)"
            />
          </div>
        </div>
      </div>

      <div class="dns-add-row">
        <b-button
          size="is-small"
          type="is-primary"
          @click="addRule"
        >+ {{ $t("dns.addRule") }}</b-button>
        <b-button
          size="is-small"
          @click="resetDefault"
          style="margin-left: 8px"
        >{{ $t("dns.resetDefault") }}</b-button>
        <b-button
          size="is-small"
          type="is-warning"
          :loading="autoSetupLoading"
          style="margin-left: 8px"
          @click="handleAutoSetup"
        >{{ $t("dns.autoSetup") }}</b-button>
      </div>
      <div v-if="autoSetupResult" class="dns-auto-result">
        <p class="dns-auto-title">
          {{ $t("dns.autoSetupResult") }}: {{ $t("dns.colServer") }}
          <strong v-if="autoSetupResult.rules && autoSetupResult.rules.length > 1">
            {{ autoSetupResult.rules[1].server }}
          </strong>
          ({{ $t("dns.direct") }}) /
          <strong v-if="autoSetupResult.rules && autoSetupResult.rules.length > 2">
            {{ autoSetupResult.rules[2].server }}
          </strong>
          ({{ $t("dns.proxy") }})
        </p>
        <b-table
          :data="autoSetupItems"
          :per-page="100"
          striped
          hoverable
          style="max-height: 240px"
        >
          <b-table-column field="kind" :label="$t('dns.kind')" width="80" v-slot="p">
            {{ p.row.kind === "direct" ? $t("dns.direct") : $t("dns.proxy") }}
          </b-table-column>
          <b-table-column field="server" :label="$t('dns.colServer')" v-slot="p">
            {{ p.row.server }}
          </b-table-column>
          <b-table-column field="latency" :label="$t('dns.latency')" width="100" v-slot="p">
            <span v-if="p.row.ok" :class="p.row.ok ? 'has-text-success' : 'has-text-danger'">
              {{ p.row.latencyMs }}ms
            </span>
            <span v-else class="has-text-danger" :title="p.row.error || ''">
              {{ p.row.error || "FAIL" }}
            </span>
          </b-table-column>
        </b-table>
      </div>
    </section>
    <footer class="modal-card-foot flex-end">
      <button class="button" @click="$emit('close')">
        {{ $t("operations.cancel") }}
      </button>
      <button class="button is-primary" @click="handleClickSubmit">
        {{ $t("operations.save") }}
      </button>
    </footer>
  </div>
</template>

<script>
import { handleResponse } from "@/assets/js/utils";

const DEFAULT_RULES = [
  { server: "localhost", domains: "geosite:private", outbound: "direct" },
  { server: "223.5.5.5", domains: "geosite:cn", outbound: "direct" },
  { server: "8.8.8.8", domains: "", outbound: "proxy" },
];

export default {
  name: "ModalDnsSetting",
  data: () => ({
    rules: DEFAULT_RULES.map((r) => ({ ...r })),
    outbounds: ["proxy"],
    autoSetupLoading: false,
    autoSetupResult: null,
  }),
  computed: {
    autoSetupItems() {
      if (!this.autoSetupResult) return [];
      const items = [];
      (this.autoSetupResult.direct || []).forEach((d) =>
        items.push({ kind: "direct", ...d })
      );
      (this.autoSetupResult.proxy || []).forEach((d) =>
        items.push({ kind: "proxy", ...d })
      );
      return items;
    },
  },
  created() {
    // Load available outbounds
    this.$axios({ url: apiRoot + "/outbounds" }).then((res) => {
      if (res.data && res.data.data && res.data.data.outbounds) {
        this.outbounds = res.data.data.outbounds;
      }
    });
    // Load current DNS rules
    this.$axios({ url: apiRoot + "/dnsRules" }).then((res) => {
      handleResponse(res, this, () => {
        if (res.data.data && res.data.data.rules && res.data.data.rules.length > 0) {
          this.rules = res.data.data.rules.map((r) => ({
            server: r.server || "",
            domains: r.domains || "",
            outbound: r.outbound || "direct",
          }));
        }
      });
    });
  },
  methods: {
    addRule() {
      this.rules.push({ server: "", domains: "", outbound: "direct" });
    },
    removeRule(index) {
      this.rules.splice(index, 1);
    },
    resetDefault() {
      this.rules = DEFAULT_RULES.map((r) => ({ ...r }));
    },
    handleAutoSetup() {
      this.autoSetupLoading = true;
      this.autoSetupResult = null;
      this.$axios({
        url: apiRoot + "/dnsAutoSetup",
        method: "post",
        timeout: 60000,
      })
        .then((res) => {
          handleResponse(res, this, () => {
            const data = res.data.data.dnsAutoSetup;
            this.autoSetupResult = data;
            if (data.rules && data.rules.length > 0) {
              this.rules = data.rules.map((r) => ({
                server: r.server || "",
                domains: r.domains || "",
                outbound: r.outbound || "direct",
              }));
              this.$buefy.toast.open({
                message: this.$t("dns.autoSetupApplied"),
                type: "is-primary",
                position: "is-top",
                duration: 3000,
                queue: false,
              });
            }
          });
        })
        .finally(() => {
          this.autoSetupLoading = false;
        });
    },
    handleClickSubmit() {
      const validRules = this.rules.filter((r) => r.server.trim() !== "");
      if (validRules.length === 0) {
        this.$buefy.toast.open({
          message: this.$t("dns.errNoRules"),
          type: "is-danger",
          position: "is-top",
        });
        return;
      }
      this.$axios({
        url: apiRoot + "/dnsRules",
        method: "put",
        data: validRules,
      }).then((res) => {
        handleResponse(res, this, () => {
          this.$emit("close");
        });
      });
    },
  },
};
</script>

<style lang="scss" scoped>
.dns-setting-modal {
  width: 900px;
  max-width: 95vw;
  max-height: calc(100vh - 2rem);
  margin: auto;

  .dns-info-msg {
    margin-bottom: 12px;
  }

  .modal-card-head {
    display: flex;
    align-items: center;
  }

  .help-link {
    display: flex;
    align-items: center;
    gap: 4px;
    font-size: 13px;
    color: #888;
    margin-left: auto;
    text-decoration: none;
    white-space: nowrap;
    &:hover {
      color: #3273dc;
    }
  }

  .dns-table {
    border: 1px solid #dbdbdb;
    border-radius: 4px;
    overflow: hidden;
  }

  .dns-row {
    display: grid;
    grid-template-columns: 220px 1fr 120px 42px;
    gap: 0;
    align-items: start;
    border-bottom: 1px solid #f0f0f0;

    &:last-child {
      border-bottom: none;
    }

    > div {
      padding: 6px 8px;
    }
  }

  .dns-header {
    background: #f8f8f8;
    font-size: 12px;
    font-weight: 600;
    color: #555;
    align-items: center;

    > div {
      padding: 8px 10px;
    }
  }

  .dns-data-row {
    background: #fff;

    &:hover {
      background: #fafafa;
    }
  }

  .col-actions {
    display: flex;
    align-items: flex-start;
    padding-top: 8px;
    justify-content: center;
  }

  .dns-domains-input ::v-deep textarea {
    min-height: 52px;
    resize: vertical;
    font-family: monospace;
    font-size: 12px;
  }

  .dns-add-row {
    margin-top: 12px;
    display: flex;
    align-items: center;
  }

  .dns-mobile-label {
    display: none;
    margin-bottom: 0.25rem;
    color: #555;
    font-size: 0.75rem;
    font-weight: 600;
  }
}

@media screen and (max-width: 720px) {
  .dns-setting-modal {
    width: calc(100vw - 1rem);
    max-width: calc(100vw - 1rem);

    .modal-card-head,
    .modal-card-foot {
      padding: 0.75rem;
    }

    .modal-card-title {
      font-size: 1.1rem;
    }

    .modal-card-body {
      padding: 0.75rem;
    }

    .dns-header {
      display: none;
    }

    .dns-row {
      display: grid;
      grid-template-columns: minmax(0, 1fr);
      padding: 0.4rem 0;
    }

    .dns-row > div {
      padding: 0.35rem 0.6rem;
    }

    .dns-mobile-label {
      display: block;
    }

    .col-actions {
      justify-content: flex-end;
      padding-top: 0;
    }

    .dns-add-row {
      flex-wrap: wrap;
      gap: 0.5rem;
    }

    .dns-add-row .button {
      margin-left: 0 !important;
    }
  }
}
</style>

<style lang="scss">
body.theme-dark {
  .dns-setting-modal {
    .help-link {
      color: var(--md-on-surface-variant);
      &:hover {
        color: var(--md-primary);
      }
    }

    .dns-table {
      border-color: var(--md-surface-variant);
    }

    .dns-row {
      border-bottom-color: var(--md-surface-variant);
    }

    .dns-header {
      background: var(--md-surface-container);
      color: var(--md-on-surface-variant);
    }

    .dns-data-row {
      background: var(--md-bg);
      &:hover {
        background: var(--md-surface-container);
      }
    }

    .dns-mobile-label {
      color: var(--md-on-surface-variant);
    }
  }
}
</style>
