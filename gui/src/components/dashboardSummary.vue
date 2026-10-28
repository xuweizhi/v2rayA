<template>
  <section class="dashboard-summary" :aria-label="$t('dashboard.summary')">
    <div class="dashboard-card">
      <span class="dashboard-label">{{ $t('dashboard.current') }}</span>
      <strong>{{ outbound }} <span class="dashboard-status">{{ runningState.running }}</span></strong>
      <span class="dashboard-node">{{ currentNode }}</span>
      <span v-if="transparentMode" class="dashboard-label">{{ $t('setting.transparentProxy') }}: {{ transparentMode }}</span>
    </div>
    <button class="dashboard-card dashboard-action" type="button" @click="$emit('traffic')">
      <span class="dashboard-label">{{ $t('traffic.live') }}</span>
      <strong>↑ {{ formatRate(traffic.up) }} <span class="dashboard-download">↓ {{ formatRate(traffic.down) }}</span></strong>
      <span>{{ $t('traffic.title') }} →</span>
    </button>
    <div class="dashboard-card dashboard-shortcuts">
      <b-button icon-left="radar" @click="$emit('diagnose')">{{ $t('diagnose.title') }}</b-button>
      <b-button icon-left="cog-outline" @click="$emit('settings')">{{ $t('dashboard.settings') }}</b-button>
    </div>
  </section>
</template>
<script>
export default {
  name: 'DashboardSummary',
  props: { traffic: { type: Object, required: true }, runningState: { type: Object, required: true }, outbound: { type: String, required: true } },
  data: () => ({ transparent: null }),
  computed: {
    currentNode() {
      const node = this.runningState.outboundToServerName[this.outbound];
      return typeof node === 'number' ? `${this.$t('common.loadBalance')} (${node})` : node || this.$t('dashboard.pending');
    },
    transparentMode() {
      const modes = { close: 'off', proxy: 'global', whitelist: 'whitelistCn', gfwlist: 'gfwlist', pac: 'sameAsPacMode' };
      return modes[this.transparent] ? this.$t(`setting.options.${modes[this.transparent]}`) : '';
    },
  },
  created() { this.refreshSetting(); },
  methods: {
    async refreshSetting() {
      try {
        const res = await this.$axios({ url: apiRoot + '/setting' });
        if (res.data.code === 'SUCCESS') this.transparent = res.data.data.setting.transparent;
      } catch (_) { this.transparent = null; }
    },
    formatRate(value) {
      let speed = Math.max(0, Number(value) || 0), unit = 0;
      const units = ['B/s', 'KB/s', 'MB/s', 'GB/s'];
      while (speed >= 1024 && unit < units.length - 1) { speed /= 1024; unit++; }
      return `${speed.toFixed(unit ? 1 : 0)} ${units[unit]}`;
    },
  },
};
</script>
<style scoped>
.dashboard-summary { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) auto; gap: 12px; margin: 0 0 22px; }
.dashboard-card { display: flex; flex-direction: column; justify-content: center; gap: 7px; min-width: 0; padding: 16px; border: 1px solid rgba(127,127,127,.22); border-radius: 10px; background: rgba(127,127,127,.04); color: inherit; text-align: left; font: inherit; }
.dashboard-label, .dashboard-node { font-size: .85rem; overflow-wrap: anywhere; }
.dashboard-status { font-size: .78rem; font-weight: 400; margin-left: 8px; }
.dashboard-action { cursor: pointer; }
.dashboard-action:hover, .dashboard-action:focus-visible { border-color: #3273dc; background: rgba(50,115,220,.06); }
.dashboard-download { margin-left: 12px; }
.dashboard-shortcuts { flex-direction: row; align-items: center; flex-wrap: wrap; }
@media (max-width: 900px) { .dashboard-summary { grid-template-columns: 1fr 1fr; } .dashboard-shortcuts { grid-column: 1 / -1; } }
@media (max-width: 520px) { .dashboard-summary { grid-template-columns: 1fr; } .dashboard-shortcuts { grid-column: auto; } }
</style>
