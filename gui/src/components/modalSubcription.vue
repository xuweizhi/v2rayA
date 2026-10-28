<template>
  <div class="modal-card" style="max-width: 540px; margin: auto">
    <header class="modal-card-head">
      <p class="modal-card-title">{{ $t("configureSubscription.title") }}</p>
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
        <b-select v-model="extra.includeProtocols" multiple expanded>
          <option v-for="protocol in protocols" :key="protocol" :value="protocol">{{ protocol }}</option>
        </b-select>
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
export default {
  name: "ModalSubscription",
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
    async previewFilters() {
      this.previewLoading = true;
      try {
        const res = await this.$axios.post(apiRoot + '/subscriptionFilterPreview', { id: this.form.id, extra: this.extra });
        handleResponse(res, this, () => { this.previewText = this.$t('subscription.previewResult', res.data.data); });
      } finally { this.previewLoading = false; }
    },
    handleClickSubmit() {
      this.form.extra.nodeDNS = this.nodeDNSText.split(/[\n,]+/).map(s => s.trim()).filter(Boolean);
      this.$emit("submit", this.form);
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
