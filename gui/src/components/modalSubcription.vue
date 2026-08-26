<template>
  <div class="modal-card" style="max-width: 400px; margin: auto">
    <header class="modal-card-head">
      <p class="modal-card-title">{{ $t("configureSubscription.title") }}</p>
    </header>
    <section class="modal-card-body">
      <b-field label="SUBSCRIPTION">
        <b-input
          v-model="which.address"
          type="textarea"
          :placeholder="$t('subscription.subscription')"
        />
      </b-field>
      <b-field label="REMARKS">
        <b-input
          v-model="which.remarks"
          :placeholder="$t('subscription.remarks')"
        />
      </b-field>
      <b-field :label="$t('subscription.decryptPassword')">
        <b-input
          v-model="which.decryptPassword"
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
      <b-field :label="$t('subscription.includeRegex')">
        <b-input v-model="extra.includeRegex" />
      </b-field>
      <b-field :label="$t('subscription.excludeRegex')">
        <b-input v-model="extra.excludeRegex" />
      </b-field>
      <b-field :label="$t('subscription.updateIntervalHour')">
        <b-input v-model.number="extra.updateIntervalHour" type="number" min="0" />
      </b-field>
      <b-field label="AUTO-SELECT">
        <b-checkbox
	  v-model="which.autoSelect"
	  >{{ $t("subscription.autoSelect") }}
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
export default {
  name: "ModalSubscription",
  props: {
    which: {
      type: Object,
      default() {
        return null;
      },
    },
  },
  computed: {
    extra() {
      if (!this.which) return {};
      if (!this.which.extra) this.which.extra = {};
      return this.which.extra;
    },
  },
  methods: {
    handleClickSubmit() {
      this.$emit("submit", this.which);
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
