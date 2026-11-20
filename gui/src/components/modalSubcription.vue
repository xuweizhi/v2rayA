<template>
  <div class="modal-card" style="max-width: 580px; width: calc(100vw - 2rem); margin: auto">
    <header class="modal-card-head">
      <p class="modal-card-title">{{ $t("configureSubscription.title") }}</p>
      <button
        type="button"
        class="delete"
        :aria-label="$t('operations.close')"
        @click="$parent.close()"
      ></button>
    </header>
    <section class="modal-card-body">
      <b-field label="SUBSCRIPTION">
        <b-input
          v-model="form.address"
          type="textarea"
          :placeholder="$t('subscription.subscription')"
        />
      </b-field>
      <b-field label="REMARKS">
        <b-input
          v-model="form.remarks"
          :placeholder="$t('subscription.remarks')"
        />
      </b-field>
      <b-field :label="$t('subscription.decryptPassword')">
        <b-input
          v-model="form.decryptPassword"
          type="password"
          password-reveal
          :placeholder="$t('import.passwordPlaceholder')"
        />
      </b-field>
      <b-field :label="$t('subscription.ageCredential')">
        <AgeCredential v-model="ageCredential"
          :disabled="clearAgeCredential" :placeholder="form.hasAgePassword ? $t('subscription.credentialSaved') : ''" />
      </b-field>
      <b-field v-if="form.hasAgePassword">
        <b-checkbox v-model="clearAgeCredential">{{ $t('subscription.clearAgeCredential') }}</b-checkbox>
      </b-field>
      <b-field :label="$t('subscription.userAgent')">
        <b-input
          v-model="extra.userAgent"
          placeholder="clash-verge"
        />
      </b-field>
      <b-field>
        <b-checkbox v-model="extra.userAgentAppend">
          {{ $t("subscription.userAgentAppend") }}
        </b-checkbox>
      </b-field>
      <b-field :label="$t('subscription.xhwid')">
        <b-input v-model="extra.xhwid" />
      </b-field>
      <b-field :label="$t('subscription.downloadStrategy')">
        <b-select v-model="extra.downloadStrategy" expanded>
          <option value="">{{ $t("subscription.strategyFollowSystem") }}</option>
          <option value="preferProxy">{{ $t("subscription.strategyPreferProxy") }}</option>
          <option value="preferDirect">{{ $t("subscription.strategyPreferDirect") }}</option>
          <option value="onlyProxy">{{ $t("subscription.strategyOnlyProxy") }}</option>
          <option value="onlyDirect">{{ $t("subscription.strategyOnlyDirect") }}</option>
        </b-select>
      </b-field>
      <b-field :label="$t('subscription.filterField')">
        <b-select v-model="extra.filterField" expanded>
          <option value="name">{{ $t('subscription.filterName') }}</option>
          <option value="host">{{ $t('subscription.filterHost') }}</option>
        </b-select>
      </b-field>
      <b-field :label="$t('subscription.protocols')" :message="$t('subscription.protocolsHelp')">
        <div class="va-chip-group" role="group" :aria-label="$t('subscription.protocols')">
          <button
            v-for="protocol in protocols"
            :key="protocol"
            type="button"
            class="va-chip"
            :class="{ 'is-selected': isProtocolSelected(protocol) }"
            :aria-pressed="String(isProtocolSelected(protocol))"
            @click="toggleProtocol(protocol)"
          >
            <i
              v-if="isProtocolSelected(protocol)"
              class="mdi mdi-check"
              aria-hidden="true"
            ></i>
            {{ protocol }}
          </button>
        </div>
      </b-field>
      <b-field :label="$t('subscription.includeRegex')">
        <b-input v-model="extra.includeRegex" />
      </b-field>
      <b-field :label="$t('subscription.excludeRegex')">
        <b-input v-model="extra.excludeRegex" />
      </b-field>
      <b-button size="is-small" :loading="previewLoading" @click="previewFilters">{{ $t('subscription.preview') }}</b-button>
      <p class="help" aria-live="polite">{{ previewText || $t('subscription.previewHelp') }}</p>
      <hr />
      <b-field><b-checkbox v-model="extra.preferNodeDNS">{{ $t('subscription.preferNodeDNS') }}</b-checkbox></b-field>
      <b-field :label="$t('subscription.nodeDNS')" :message="$t('subscription.nodeDNSHelp')">
        <b-input v-model="nodeDNSText" type="textarea" :disabled="!extra.preferNodeDNS" placeholder="1.1.1.1&#10;https://dns.google/dns-query" />
      </b-field>
      <p v-if="extra.providerNodeDNS && extra.providerNodeDNS.length" class="help" style="overflow-wrap: anywhere">
        {{ $t('subscription.providerDNS') }}: {{ extra.providerNodeDNS.join(', ') }}
      </p>
      <b-field :label="$t('subscription.updateIntervalHour')">
        <b-input v-model.number="extra.updateIntervalHour" type="number" min="0" />
      </b-field>
      <b-field label="AUTO-SELECT">
        <b-checkbox v-model="form.autoSelect">
          {{ $t("subscription.autoSelect") }}
        </b-checkbox>
      </b-field>
    </section>
    <footer class="modal-card-foot flex-end">
      <button class="button" type="button" @click="$parent.close()">
        {{ $t("operations.cancel") }}
      </button>
      <button class="button is-primary" @click="handleClickSubmit">
        {{ $t("operations.saveApply") }}
      </button>
    </footer>
  </div>
</template>

<script>
import { handleResponse } from "@/assets/js/utils";
import AgeCredential from "./AgeCredential.vue";
export default {
  name: "ModalSubscription",
  components: { AgeCredential },
  props: {
    which: {
      type: Object,
      default() {
        return {};
      },
    },
  },
  data() {
    const source = this.which || {};
    return {
      protocols: ['vmess', 'vless', 'trojan', 'shadowsocks', 'shadowsocksr', 'hysteria2', 'tuic', 'anytls', 'mieru', 'wireguard', 'shadowtls', 'juicity', 'socks5', 'http'],
      ageCredential: "",
      clearAgeCredential: false,
      previewLoading: false,
      previewText: '',
      nodeDNSText: ((source.extra || {}).nodeDNS || []).join('\n'),
      form: {
        ...source,
        extra: { filterField: "name", includeProtocols: [], preferNodeDNS: false, ...(source.extra || {}) },
      },
    };
  },
  computed: {
    extra() {
      return this.form.extra;
    },
  },
  methods: {
    isProtocolSelected(protocol) {
      return Array.isArray(this.extra.includeProtocols) && this.extra.includeProtocols.includes(protocol);
    },
    toggleProtocol(protocol) {
      if (!Array.isArray(this.extra.includeProtocols)) {
        this.$set(this.extra, "includeProtocols", []);
      }
      const idx = this.extra.includeProtocols.indexOf(protocol);
      if (idx >= 0) {
        this.extra.includeProtocols.splice(idx, 1);
      } else {
        this.extra.includeProtocols.push(protocol);
      }
    },
    async previewFilters() {
      this.previewLoading = true;
      try {
        const res = await this.$axios.post(apiRoot + '/subscriptionFilterPreview', { id: this.form.id, extra: this.extra });
        handleResponse(res, this, () => { this.previewText = this.$t('subscription.previewResult', res.data.data); });
      } finally { this.previewLoading = false; }
    },
    handleClickSubmit() {
      this.form.extra.nodeDNS = this.nodeDNSText.split(/[\n,]+/).map(s => s.trim()).filter(Boolean);
      this.$emit("submit", { ...this.form, agePassword: this.clearAgeCredential ? "" : (this.ageCredential || undefined) });
    },
  },
};
</script>

<style lang="scss">
.is-twitter .is-active a {
  color: #4099ff !important;
}
.readonly {
  pointer-events: none;
}
.same-width-5 li {
  width: 5em;
}
</style>
