<template>
  <section
    class="dashboard-summary"
    :class="{ 'is-novice': noviceMode }"
    :aria-label="$t('dashboard.summary')"
  >
    <div class="dashboard-card dashboard-card--status">
      <div class="dashboard-card-top">
        <span class="dashboard-label">{{ $t("dashboard.current") }}</span>
        <span class="dashboard-status-pill" :class="statusPillClass">
          <span class="dashboard-status-dot"></span>
          {{ runningState.running }}
        </span>
      </div>
      <div class="dashboard-group-line">
        <strong class="dashboard-group-name">{{ outbound.toUpperCase() }}</strong>
        <span v-if="transparentMode" class="dashboard-mode-chip">
          {{ transparentMode }}
        </span>
      </div>
      <div class="dashboard-node-row">
        <span class="dashboard-node" :title="currentNode">{{ currentNode }}</span>
        <button
          v-if="connectedCount > 0"
          type="button"
          class="dashboard-inline-btn"
          @click="$emit('active-nodes')"
        >
          <i class="mdi mdi-server-network" aria-hidden="true"></i>
          {{ $t("dashboard.activeNodes", { count: connectedCount }) }}
        </button>
      </div>
    </div>

    <button
      v-if="!noviceMode"
      class="dashboard-card dashboard-action"
      type="button"
      @click="$emit('traffic')"
    >
      <div class="dashboard-card-top">
        <span class="dashboard-label">{{ $t("traffic.live") }}</span>
        <span class="dashboard-cta">{{ $t("traffic.title") }} →</span>
      </div>
      <div class="dashboard-rates">
        <span class="dashboard-rate is-upload">
          <i class="mdi mdi-arrow-up" aria-hidden="true"></i>
          {{ formatRate(traffic.up) }}
        </span>
        <span class="dashboard-rate is-download">
          <i class="mdi mdi-arrow-down" aria-hidden="true"></i>
          {{ formatRate(traffic.down) }}
        </span>
      </div>
    </button>

    <div class="dashboard-card dashboard-shortcuts">
      <b-button size="is-small" icon-left="radar" @click="$emit('diagnose')">
        {{ $t("diagnose.title") }}
      </b-button>
      <b-button size="is-small" icon-left="cog-outline" @click="$emit('settings')">
        {{ $t("dashboard.settings") }}
      </b-button>
      <b-button size="is-small" icon-left="text-box-outline" @click="$emit('logs')">
        {{ $t("common.log") }}
      </b-button>
    </div>
  </section>
</template>

<script>
export default {
  name: "DashboardSummary",
  props: {
    traffic: { type: Object, required: true },
    runningState: { type: Object, required: true },
    outbound: { type: String, required: true },
    noviceMode: { type: Boolean, default: false },
    connectedCount: { type: Number, default: 0 },
  },
  data: () => ({ transparent: null }),
  computed: {
    currentNode() {
      const node = this.runningState.outboundToServerName[this.outbound];
      return typeof node === "number"
        ? `${this.$t("common.loadBalance")} (${node})`
        : node || this.$t("dashboard.pending");
    },
    transparentMode() {
      const modes = {
        close: "off",
        proxy: "global",
        whitelist: "whitelistCn",
        gfwlist: "gfwlist",
        pac: "sameAsPacMode",
      };
      return modes[this.transparent]
        ? this.$t(`setting.options.${modes[this.transparent]}`)
        : "";
    },
    statusPillClass() {
      const st = this.runningState.status;
      if (st === "running" || this.runningState.running === this.$t("common.isRunning")) {
        return "is-running";
      }
      if (st === "waiting" || this.runningState.running === this.$t("common.waitingNetwork")) {
        return "is-waiting";
      }
      if (st === "stopped" || this.runningState.running === this.$t("common.notRunning")) {
        return "is-idle";
      }
      return "is-checking";
    },
  },
  created() {
    this.refreshSetting();
  },
  methods: {
    async refreshSetting() {
      if (!localStorage["token"]) return;
      try {
        const res = await this.$axios({ url: apiRoot + "/setting" });
        if (res.data.code === "SUCCESS") {
          this.transparent = res.data.data.setting.transparent;
        }
      } catch (_) {
        this.transparent = null;
      }
    },
    formatRate(value) {
      let speed = Math.max(0, Number(value) || 0);
      let unit = 0;
      const units = ["B/s", "KB/s", "MB/s", "GB/s"];
      while (speed >= 1024 && unit < units.length - 1) {
        speed /= 1024;
        unit++;
      }
      return `${speed.toFixed(unit ? 1 : 0)} ${units[unit]}`;
    },
  },
};
</script>

<style lang="scss" scoped>
.dashboard-summary {
  display: grid;
  grid-template-columns: minmax(0, 1.35fr) minmax(0, 1fr) auto;
  gap: 0.75rem;
  margin: 0 0 1.25rem;

  &.is-novice {
    grid-template-columns: minmax(0, 1fr) auto;
  }
}

.dashboard-card {
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: 0.45rem;
  min-width: 0;
  padding: 0.9rem 1.05rem;
  border: 1px solid var(--va-border);
  border-radius: var(--va-radius-md);
  background: var(--va-surface);
  box-shadow: var(--va-shadow-sm);
  color: var(--va-text);
  text-align: left;
  font: inherit;
  transition: border-color 0.16s ease, box-shadow 0.16s ease;
}

.dashboard-card-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
}

.dashboard-label {
  font-size: 0.78rem;
  font-weight: 500;
  color: var(--va-text-muted);
}

.dashboard-group-line {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 0.5rem;
}

.dashboard-group-name {
  font-size: 1.05rem;
  font-weight: 700;
  letter-spacing: 0.02em;
  color: var(--va-text);
}

.dashboard-mode-chip {
  display: inline-flex;
  align-items: center;
  padding: 0.12rem 0.5rem;
  border-radius: 999px;
  font-size: 0.72rem;
  font-weight: 500;
  background: var(--va-surface-subtle);
  border: 1px solid var(--va-border);
  color: var(--va-text-muted);
}

.dashboard-status-pill {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  padding: 0.16rem 0.55rem;
  border-radius: 999px;
  font-size: 0.74rem;
  font-weight: 600;
  white-space: nowrap;

  &.is-running {
    background: var(--va-success-soft);
    color: var(--va-success);
  }

  &.is-waiting {
    background: var(--va-warning-soft);
    color: var(--va-warning);
  }

  &.is-idle,
  &.is-checking {
    background: var(--va-surface-subtle);
    color: var(--va-text-muted);
  }
}

.dashboard-status-dot {
  width: 0.45rem;
  height: 0.45rem;
  border-radius: 50%;
  background: currentColor;
}

.dashboard-node-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  min-width: 0;
}

.dashboard-node {
  font-size: 0.85rem;
  color: var(--va-text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dashboard-inline-btn {
  appearance: none;
  border: 1px solid var(--va-border);
  background: var(--va-surface-subtle);
  color: var(--va-primary);
  border-radius: 999px;
  padding: 0.16rem 0.55rem;
  font-size: 0.74rem;
  font-weight: 600;
  cursor: pointer;
  white-space: nowrap;
  display: inline-flex;
  align-items: center;
  gap: 0.25rem;
  transition: background-color 0.15s ease, border-color 0.15s ease;

  &:hover {
    background: var(--va-primary-soft);
    border-color: var(--va-primary);
  }
}

.dashboard-action {
  cursor: pointer;

  &:hover,
  &:focus-visible {
    border-color: var(--va-primary);
    box-shadow: var(--va-shadow-md);
  }
}

.dashboard-cta {
  font-size: 0.76rem;
  font-weight: 600;
  color: var(--va-primary);
}

.dashboard-rates {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 1rem;
  margin-top: 0.15rem;
  font-variant-numeric: tabular-nums;
}

.dashboard-rate {
  display: inline-flex;
  align-items: center;
  gap: 0.2rem;
  font-size: 1.05rem;
  font-weight: 700;

  &.is-upload i {
    color: var(--va-success);
  }

  &.is-download i {
    color: var(--va-primary);
  }
}

.dashboard-shortcuts {
  flex-direction: row;
  align-items: center;
  flex-wrap: wrap;
  gap: 0.5rem;
}

@media (max-width: 960px) {
  .dashboard-summary,
  .dashboard-summary.is-novice {
    grid-template-columns: 1fr 1fr;
  }

  .dashboard-shortcuts {
    grid-column: 1 / -1;
  }
}

@media (max-width: 560px) {
  .dashboard-summary,
  .dashboard-summary.is-novice {
    grid-template-columns: 1fr;
  }

  .dashboard-shortcuts {
    grid-column: auto;
  }
}
</style>
