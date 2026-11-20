<template>
  <div class="modal-card" style="max-width: 740px; width: calc(100vw - 2rem); margin: auto">
    <header class="modal-card-head">
      <p class="modal-card-title">{{ $t("routingRules.title") }}</p>
      <button
        type="button"
        class="delete"
        :aria-label="$t('operations.close')"
        @click="$parent.close()"
      ></button>
    </header>
    <section class="modal-card-body">
      <b-message type="is-info" size="is-small">
        {{ $t("routingRules.note") }}
      </b-message>
      <div v-if="!rules.length" class="va-empty-state">
        {{ $t("ruleOrder.empty") }}
      </div>
      <div v-for="(rule, index) of rules" :key="index" class="card rr-card">
        <div class="card-content">
          <div class="rr-head">
            <strong>#{{ index + 1 }}</strong>
            <div class="rr-head-actions">
              <b-button
                size="is-small"
                icon-left="arrow-up"
                :disabled="index === 0"
                :title="$t('ruleOrder.moveUp')"
                :aria-label="$t('ruleOrder.moveUp')"
                @click="moveRule(index, -1)"
              />
              <b-button
                size="is-small"
                icon-left="arrow-down"
                :disabled="index === rules.length - 1"
                :title="$t('ruleOrder.moveDown')"
                :aria-label="$t('ruleOrder.moveDown')"
                @click="moveRule(index, 1)"
              />
              <b-button type="is-danger" outlined size="is-small" icon-left="delete-outline" @click="rules.splice(index, 1)">
                {{ $t("operations.delete") }}
              </b-button>
            </div>
          </div>
          <b-field :label="$t('routingRules.domains')" label-position="on-border">
            <b-input
              v-model="rule.domainText"
              type="textarea"
              rows="2"
              :placeholder="$t('routingRules.domainPlaceholder')"
              custom-class="code-font"
            />
          </b-field>
          <div class="columns">
            <div class="column is-5">
              <b-field :label="$t('connections.outbound')" label-position="on-border">
                <b-select v-model="rule.outbound" expanded size="is-small">
                  <option value="direct">direct</option>
                  <option value="block">block</option>
                  <option value="proxy">proxy</option>
                  <option v-for="out in outbounds" :key="out" :value="out">{{ out }}</option>
                </b-select>
              </b-field>
            </div>
            <div class="column is-3">
              <b-field :label="$t('detectRule.port')" label-position="on-border">
                <b-input v-model="rule.port" placeholder="443 / 1000-2000" size="is-small" />
              </b-field>
            </div>
            <div class="column is-4">
              <b-field :label="$t('detectRule.network')" label-position="on-border">
                <b-select v-model="rule.network" expanded size="is-small">
                  <option value=""></option>
                  <option value="tcp">tcp</option>
                  <option value="udp">udp</option>
                  <option value="tcp,udp">tcp+udp</option>
                </b-select>
              </b-field>
            </div>
          </div>
        </div>
      </div>
      <b-button size="is-small" type="is-primary" @click="addRule">+ {{ $t("dns.addRule") }}</b-button>
    </section>
    <footer class="modal-card-foot flex-end">
      <button class="button" type="button" @click="$parent.close()">
        {{ $t("operations.cancel") }}
      </button>
      <button class="button is-primary" @click="handleSave">
        {{ $t("operations.saveApply") }}
      </button>
    </footer>
  </div>
</template>

<script>
import { handleResponse } from "@/assets/js/utils";
export default {
  name: "ModalRoutingRules",
  data: () => ({
    rules: [],
    outbounds: ["proxy"],
  }),
  created() {
    this.$axios({ url: apiRoot + "/outbounds", method: "get" }).then((res) => {
      if (res.data && res.data.code === "SUCCESS" && res.data.data) {
        this.outbounds = (res.data.data.outbounds || []).filter(
          (o) => o !== "direct" && o !== "block"
        );
      }
    });
    this.$axios({ url: apiRoot + "/setting", method: "get" }).then((res) => {
      if (res.data && res.data.code === "SUCCESS" && res.data.data) {
        const rules = res.data.data.setting.routingRules || [];
        this.rules = rules.map((r) => ({
          outbound: r.outbound || "proxy",
          domainText: (r.domain || []).join("\n"),
          ip: r.ip || [],
          port: r.port || "",
          network: r.network || "",
        }));
      }
    });
  },
  methods: {
    addRule() {
      this.rules.push({
        outbound: "proxy",
        domainText: "",
        ip: [],
        port: "",
        network: "",
      });
    },
    moveRule(index, delta) {
      const target = index + delta;
      if (target < 0 || target >= this.rules.length) return;
      const item = this.rules[index];
      this.rules.splice(index, 1);
      this.rules.splice(target, 0, item);
    },
    handleSave() {
      const routingRules = [];
      let skippedEmpty = 0;
      for (const rule of this.rules) {
        const domains = (rule.domainText || "")
          .split(/\n|,/)
          .map((s) => s.trim())
          .filter(Boolean);
        const item = { outbound: rule.outbound || "proxy" };
        if (domains.length) item.domain = domains;
        if ((rule.ip || []).length) item.ip = rule.ip;
        if (rule.port) item.port = rule.port;
        if (rule.network) item.network = rule.network;
        if (!item.domain && !item.ip && !item.port) {
          skippedEmpty++;
          continue;
        }
        routingRules.push(item);
      }
      this.$axios({ url: apiRoot + "/setting", method: "get" }).then((res) => {
        const s = Object.assign({}, res.data.data.setting);
        s.routingRules = routingRules;
        this.$axios({
          url: apiRoot + "/setting",
          method: "put",
          data: s,
        }).then((res2) => {
          handleResponse(res2, this, () => {
            this.$buefy.toast.open({
              message:
                skippedEmpty > 0
                  ? `${this.$t("common.success")} — ${this.$t("ruleOrder.skippedEmpty", { count: skippedEmpty })}`
                  : this.$t("common.success"),
              type: skippedEmpty > 0 ? "is-warning" : "is-success",
              position: "is-top",
              duration: 3500,
              queue: false,
            });
            this.$parent.close();
          });
        });
      });
    },
  },
};
</script>

<style scoped>
.rr-card {
  margin-bottom: 0.75rem;
}
.rr-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 0.55rem;
}

.rr-head-actions {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
}
</style>
