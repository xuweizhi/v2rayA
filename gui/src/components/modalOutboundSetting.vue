<template>
  <div class="modal-card" style="max-width: 480px; width: calc(100vw - 2rem); margin: auto">
    <header class="modal-card-head">
      <p class="modal-card-title">
        {{ outbound }} - {{ $t("common.outboundSetting") }}
      </p>
      <button
        type="button"
        class="delete"
        :aria-label="$t('operations.close')"
        @click="$emit('close')"
      ></button>
    </header>
    <section class="modal-card-body">
      <b-field :label="$t('outbound.preProxy')" :message="$t('outbound.preProxyHelp')">
        <b-select v-model="setting.preProxy" expanded>
          <option value="">{{ $t('outbound.noPreProxy') }}</option>
          <option v-for="group in groups.filter(g => g !== outbound)" :key="group" :value="group">{{ group }}</option>
        </b-select>
      </b-field>
      <b-field>
        <b-switch :value="!!setting.memberRule" :disabled="!backendReady" @input="toggleRule">
          {{ $t("outbound.automaticMembers") }}
        </b-switch>
      </b-field>
      <template v-if="setting.memberRule">
        <b-field :label="$t('outbound.sourceSubscriptions')">
          <div>
            <label v-for="source in sourceOptions" :key="source.id" class="member-source">
              <input type="checkbox" v-model="setting.memberRule.subscriptionIDs" :value="source.id">
              #{{ source.id }} {{ source.label }}
            </label>
            <span v-if="!sourceOptions.length">{{ $t("outbound.noSources") }}</span>
          </div>
        </b-field>
        <b-field :label="$t('outbound.nameMatch')">
          <b-select v-model="setting.memberRule.mode">
            <option value="keyword">{{ $t("outbound.keyword") }}</option>
            <option value="regex">{{ $t("outbound.regex") }}</option>
          </b-select>
          <b-input v-model="setting.memberRule.pattern" :aria-label="$t('outbound.nameMatch')" expanded />
        </b-field>
        <b-button :disabled="!ruleReady" @click="previewMembers">{{ $t("outbound.previewMembers") }}</b-button>
        <div v-if="preview" aria-live="polite" class="member-preview">
          <p>{{ $t("outbound.matched", { count: preview.matched }) }}</p>
          <p v-if="preview.keepPrevious">{{ $t("outbound.keepPrevious") }}</p>
          <p v-if="preview.members && preview.members.length">{{ $t("outbound.membersAfterSave") }} · {{ preview.members.join(", ") }}</p>
          <p v-if="preview.added && preview.added.length">{{ $t("outbound.addMembers") }} · {{ preview.added.join(", ") }}</p>
          <p v-if="preview.removed && preview.removed.length">{{ $t("outbound.removeMembers") }} · {{ preview.removed.join(", ") }}</p>
        </div>
      </template>
      <b-field label="ProbeURL" label-position="on-border">
        <b-input ref="probe_url" v-model="setting.probeURL" required expanded />
      </b-field>
      <b-field label="ProbeInterval" label-position="on-border">
        <b-input
          ref="probe_interval"
          v-model="setting.probeInterval"
          required
          expanded
        />
      </b-field>
      <b-field label="Type" label-position="on-border">
        <b-select v-model="setting.type" expanded>
          <option value="leastping">
            {{ $t("setting.options.leastPing") }}
          </option>
        </b-select>
      </b-field>
    </section>
    <footer class="modal-card-foot outbound-setting-foot">
      <button class="button is-danger is-outlined" type="button" @click="handleClickDelete">
        {{ $t("operations.delete") }}
      </button>
      <div class="outbound-setting-foot__right">
        <button class="button" type="button" @click="$emit('close')">
          {{ $t("operations.cancel") }}
        </button>
        <button class="button is-primary" :disabled="!backendReady || !ruleReady" @click="handleClickSubmit">
          {{ $t("operations.confirm") }}
        </button>
      </div>
    </footer>
  </div>
</template>

<script>
import { handleResponse } from "../assets/js/utils";
import i18n from "@/plugins/i18n";

export default {
  name: "ModalOutboundSetting",
  i18n,
  props: {
    outbound: {
      type: String,
      required: true,
    },
  },
  data: () => ({
    groups: [],
    setting: {
      memberRule: null,
      preProxy: "",
      probeURL: "",
      probeInterval: "",
      type: "",
    },
    backendReady: false,
    sources: [],
    preview: null,
    previewSequence: 0,
  }),
  computed: {
    sourceOptions() {
      const options = this.sources.slice();
      for (const id of this.setting.memberRule.subscriptionIDs) {
        if (!options.some(source => source.id === id)) {
          options.push({ id, label: this.$t("outbound.missingSource") });
        }
      }
      return options;
    },
    ruleReady() {
      const rule = this.setting.memberRule;
      return !rule || (rule.pattern.trim() && rule.subscriptionIDs.length > 0 &&
        rule.subscriptionIDs.every(id => this.sources.some(source => source.id === id)));
    },
  },
  watch: {
    "setting.memberRule": {
      deep: true,
      handler() {
        this.preview = null;
        this.previewSequence++;
      },
    },
  },
  created() {
    this.$axios.get(apiRoot + '/outbounds').then(res => handleResponse(res, this, () => { this.groups = res.data.data.outbounds; }));
    this.$axios({
      url: apiRoot + "/outbound",
      params: {
        outbound: this.outbound,
      },
    }).then((res) => {
      handleResponse(res, this, () => {
        Object.assign(this.setting, res.data.data.setting);
        this.sources = res.data.data.subscriptionSources || [];
        this.backendReady = true;
      });
    });
  },
  methods: {
    toggleRule(enabled) {
      this.setting.memberRule = enabled ? { subscriptionIDs: [], mode: "keyword", pattern: "" } : null;
    },
    previewMembers() {
      const sequence = ++this.previewSequence;
      this.$axios.post(apiRoot + "/outboundMemberPreview", {
        outbound: this.outbound,
        memberRule: this.setting.memberRule,
      }).then(res => {
        if (sequence === this.previewSequence) {
          handleResponse(res, this, () => { this.preview = res.data.data; });
        }
      });
    },
    handleClickDelete() {
      const that = this;
      this.$buefy.dialog.confirm({
        title: that.$t("delete.title"),
        message: that.$t("outbound.deleteMessage", {
          outboundName: that.outbound,
        }),
        confirmText: that.$t("operations.delete"),
        cancelText: that.$t("operations.cancel"),
        type: "is-danger",
        hasIcon: true,
        icon: "alert-circle-outline",
        onConfirm: () => {
          that.$emit("delete");
          that.$parent.close();
        },
      });
    },
    handleClickSubmit() {
      let valid = true;
      for (let k in this.$refs) {
        if (!Object.prototype.hasOwnProperty.call(this.$refs, k)) {
          continue;
        }
        let x = this.$refs[k];
        if (!x) {
          continue;
        }
        if (
          x.$el.offsetParent && // is visible
          typeof x.checkHtml5Validity === "function" &&
          !x.checkHtml5Validity()
        ) {
          console.error("validate failed", x);
          valid = false;
        }
      }
      if (!valid) {
        return;
      }
      this.$axios({
        url: apiRoot + "/outbound",
        method: "put",
        data: {
          outbound: this.outbound,
          setting: this.setting,
        },
      }).then((res) => {
        handleResponse(res, this, () => {
          this.$buefy.toast.open({
            message: this.$t("common.success"),
            type: "is-success",
            position: "is-top",
            queue: false,
          });
          this.$emit("saved");
          this.$parent.close();
        });

      });
    },
  },
};
</script>

<style scoped>
.member-source { display: block; }
.member-preview { margin: .5rem 0 1rem; overflow-wrap: anywhere; }
.outbound-setting-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
}
.outbound-setting-foot__right {
  flex: 1;
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
}
</style>

<style lang="scss">
.modal-custom-ports .modal-background {
  background-color: rgba(0, 0, 0, 0.6);
}
</style>
