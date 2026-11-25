<template>
  <section
    class="dashboard-summary"
    :class="{ 'is-novice': noviceMode }"
    :aria-label="$t('dashboard.summary')"
  >
    <div class="dashboard-card dashboard-card--status" :class="`state-${statusPillClass}`">
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
          <i class="mdi mdi-shield-outline" aria-hidden="true"></i>
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
      class="dashboard-card dashboard-action dashboard-card--traffic"
      type="button"
      @click="$emit('traffic')"
    >
      <div class="dashboard-card-top">
        <span class="dashboard-label dashboard-live-label">
          <span class="dashboard-live-dot" aria-hidden="true"></span>
          {{ $t("traffic.live") }}
        </span>
        <span class="dashboard-cta">{{ $t("traffic.title") }} →</span>
      </div>
      <div class="dashboard-rates">
        <span class="dashboard-rate is-upload">
          <span class="rate-badge"><i class="mdi mdi-arrow-up" aria-hidden="true"></i></span>
          {{ formatRate(traffic.up) }}
        </span>
        <span class="dashboard-rate is-download">
          <span class="rate-badge"><i class="mdi mdi-arrow-down" aria-hidden="true"></i></span>
          {{ formatRate(traffic.down) }}
        </span>
      </div>
      <svg
        class="dashboard-sparkline"
        viewBox="0 0 240 44"
        preserveAspectRatio="none"
        aria-hidden="true"
      >
        <defs>
          <linearGradient id="dash-down-grad" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" style="stop-color: var(--va-spark-down, #3b6cf5)" stop-opacity="0.34" />
            <stop offset="100%" style="stop-color: var(--va-spark-down, #3b6cf5)" stop-opacity="0.0" />
          </linearGradient>
          <linearGradient id="dash-up-grad" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" style="stop-color: var(--va-spark-up, #10b981)" stop-opacity="0.3" />
            <stop offset="100%" style="stop-color: var(--va-spark-up, #10b981)" stop-opacity="0.0" />
          </linearGradient>
        </defs>
        <line x1="0" y1="42" x2="240" y2="42" class="sparkline-baseline" />
        <polygon v-if="downArea" :points="downArea" fill="url(#dash-down-grad)" />
        <polygon v-if="upArea" :points="upArea" fill="url(#dash-up-grad)" />
        <polyline v-if="downLine" :points="downLine" class="sparkline-stroke is-download" />
        <polyline v-if="upLine" :points="upLine" class="sparkline-stroke is-upload" />
      </svg>
    </button>

    <div class="dashboard-card dashboard-shortcuts">
      <button type="button" class="shortcut-btn" @click="$emit('diagnose')">
        <span class="shortcut-btn__icon is-info">
          <i class="mdi mdi-radar" aria-hidden="true"></i>
        </span>
        <span>{{ $t("diagnose.title") }}</span>
      </button>
      <button type="button" class="shortcut-btn" @click="$emit('settings')">
        <span class="shortcut-btn__icon is-primary">
          <i class="mdi mdi-cog-outline" aria-hidden="true"></i>
        </span>
        <span>{{ $t("dashboard.settings") }}</span>
      </button>
      <button type="button" class="shortcut-btn" @click="$emit('logs')">
        <span class="shortcut-btn__icon is-neutral">
          <i class="mdi mdi-text-box-outline" aria-hidden="true"></i>
        </span>
        <span>{{ $t("common.log") }}</span>
      </button>
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
      const map = this.runningState.outboundToServerName || {};
      if (!Object.prototype.hasOwnProperty.call(map, this.outbound)) {
        return this.$t("dashboard.pending");
      }
      const node = map[this.outbound];
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
    recentSamples() {
      const list = Array.isArray(this.traffic?.samples) ? this.traffic.samples : [];
      return list.slice(-36);
    },
    sparkMax() {
      let max = 1024;
      for (const s of this.recentSamples) {
        max = Math.max(max, Number(s.up) || 0, Number(s.down) || 0);
      }
      return max;
    },
    downLine() {
      return this.buildSparkPoints("down");
    },
    upLine() {
      return this.buildSparkPoints("up");
    },
    downArea() {
      return this.buildSparkArea(this.downLine);
    },
    upArea() {
      return this.buildSparkArea(this.upLine);
    },
  },
  created() {
    this.refreshSetting();
  },
  methods: {
    buildSparkPoints(field) {
      const samples = this.recentSamples;
      if (samples.length < 2) return "";
      const width = 240;
      const top = 4;
      const bottom = 42;
      const span = bottom - top;
      const totalSlots = 36;
      const offset = totalSlots - samples.length;
      return samples
        .map((s, idx) => {
          const x = ((offset + idx) / (totalSlots - 1)) * width;
          const val = Math.max(0, Number(s[field]) || 0);
          const y = bottom - Math.min(val / this.sparkMax, 1) * span;
          return `${x.toFixed(1)},${y.toFixed(1)}`;
        })
        .join(" ");
    },
    buildSparkArea(points) {
      if (!points) return "";
      const segs = points.split(" ");
      const firstX = segs[0].split(",")[0];
      const lastX = segs[segs.length - 1].split(",")[0];
      return `${firstX},42 ${points} ${lastX},42`;
    },
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
  grid-template-columns: minmax(0, 1.3fr) minmax(0, 1.15fr) auto;
  gap: 0.85rem;
  margin: 0 0 1.2rem;

  &.is-novice {
    grid-template-columns: minmax(0, 1fr) auto;
  }
}

.dashboard-card {
  position: relative;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: 0.5rem;
  min-width: 0;
  padding: 1rem 1.15rem;
  border: 1px solid var(--va-border);
  border-radius: var(--va-radius-md);
  background: var(--va-surface);
  box-shadow: var(--va-shadow-sm);
  color: var(--va-text);
  text-align: left;
  font: inherit;
  transition: transform 0.16s ease, border-color 0.16s ease, box-shadow 0.18s ease;
}

.dashboard-card--status.state-is-running {
  background: radial-gradient(
      circle at 100% 0%,
      rgba(16, 185, 129, 0.1) 0%,
      transparent 55%
    ),
    var(--va-surface);
  border-color: rgba(16, 185, 129, 0.32);
}

.dashboard-card--traffic {
  padding-bottom: 0.65rem;
  background: radial-gradient(
      circle at 90% 0%,
      rgba(59, 108, 245, 0.08) 0%,
      transparent 55%
    ),
    var(--va-surface);
}

.dashboard-card-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
}

.dashboard-label {
  font-size: 0.74rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--va-text-subtle);
}

.dashboard-live-label {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
}

.dashboard-live-dot {
  width: 0.45rem;
  height: 0.45rem;
  border-radius: 50%;
  background: var(--va-success);
  animation: va-status-pulse 2.2s infinite;
}

.dashboard-group-line {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 0.5rem;
}

.dashboard-group-name {
  font-size: 1.22rem;
  font-weight: 800;
  letter-spacing: -0.015em;
  color: var(--va-text);
}

.dashboard-mode-chip {
  display: inline-flex;
  align-items: center;
  gap: 0.25rem;
  padding: 0.14rem 0.55rem;
  border-radius: 999px;
  font-size: 0.73rem;
  font-weight: 500;
  background: var(--va-surface-subtle);
  border: 1px solid var(--va-border);
  color: var(--va-text-muted);
}

.dashboard-status-pill {
  display: inline-flex;
  align-items: center;
  gap: 0.38rem;
  padding: 0.18rem 0.62rem;
  border-radius: 999px;
  font-size: 0.74rem;
  font-weight: 600;
  white-space: nowrap;

  &.is-running {
    background: var(--va-success-soft);
    color: var(--va-success-text, var(--va-success));

    .dashboard-status-dot {
      background: var(--va-success);
      animation: va-status-pulse 2.2s infinite;
    }
  }

  &.is-waiting {
    background: var(--va-warning-soft);
    color: var(--va-warning-text, var(--va-warning));
  }

  &.is-idle,
  &.is-checking {
    background: var(--va-surface-subtle);
    color: var(--va-text-muted);
  }
}

.dashboard-status-dot {
  width: 0.46rem;
  height: 0.46rem;
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
  font-size: 0.86rem;
  font-weight: 500;
  color: var(--va-text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dashboard-inline-btn {
  appearance: none;
  border: 1px solid rgba(59, 108, 245, 0.28);
  background: var(--va-primary-soft);
  color: var(--va-primary);
  border-radius: 999px;
  padding: 0.18rem 0.62rem;
  font-size: 0.74rem;
  font-weight: 600;
  cursor: pointer;
  white-space: nowrap;
  display: inline-flex;
  align-items: center;
  gap: 0.28rem;
  transition: transform 0.14s ease, background-color 0.15s ease, color 0.15s ease;

  &:hover {
    background: var(--va-primary);
    color: #ffffff;
    transform: translateY(-1px);
  }
}

.dashboard-action {
  cursor: pointer;

  &:hover,
  &:focus-visible {
    border-color: var(--va-primary);
    box-shadow: var(--va-shadow-md);
    transform: translateY(-1px);
  }
}

.dashboard-cta {
  font-size: 0.76rem;
  font-weight: 600;
  color: var(--va-primary);
}

.dashboard-rates {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 1.1rem;
  font-variant-numeric: tabular-nums;
}

.dashboard-rate {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
  font-size: 1.12rem;
  font-weight: 700;
  letter-spacing: -0.01em;

  .rate-badge {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 1.35rem;
    height: 1.35rem;
    border-radius: 6px;
    font-size: 0.86rem;
  }

  &.is-upload .rate-badge {
    background: var(--va-success-soft);
    color: var(--va-success);
  }

  &.is-download .rate-badge {
    background: var(--va-primary-soft);
    color: var(--va-primary);
  }
}

.dashboard-sparkline {
  display: block;
  width: 100%;
  height: 36px;
  margin-top: 0.1rem;
  overflow: visible;
}

.sparkline-baseline {
  stroke: var(--va-border);
  stroke-width: 1;
  stroke-dasharray: 2 3;
}

.sparkline-stroke {
  fill: none;
  stroke-width: 1.8;
  stroke-linecap: round;
  stroke-linejoin: round;
  vector-effect: non-scaling-stroke;

  &.is-upload {
    stroke: var(--va-spark-up, #10b981);
  }

  &.is-download {
    stroke: var(--va-spark-down, #3b6cf5);
  }
}

.dashboard-shortcuts {
  flex-direction: row;
  align-items: center;
  flex-wrap: wrap;
  gap: 0.5rem;
  padding: 0.75rem 0.9rem;
}

.shortcut-btn {
  appearance: none;
  display: inline-flex;
  align-items: center;
  gap: 0.48rem;
  padding: 0.48rem 0.78rem;
  border-radius: var(--va-radius-sm);
  border: 1px solid var(--va-border);
  background: linear-gradient(180deg, var(--va-btn-top) 0%, var(--va-btn-bottom) 100%);
  color: var(--va-text);
  font: inherit;
  font-size: 0.84rem;
  font-weight: 600;
  cursor: pointer;
  white-space: nowrap;
  box-shadow: var(--va-shadow-xs);
  transition: transform 0.14s ease, border-color 0.15s ease, box-shadow 0.16s ease, color 0.15s ease;

  &:hover {
    border-color: var(--va-primary);
    color: var(--va-primary);
    transform: translateY(-1px);
    box-shadow: 0 4px 12px rgba(15, 23, 42, 0.08);
  }

  &:active {
    transform: scale(0.98);
  }
}

.shortcut-btn__icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 1.55rem;
  height: 1.55rem;
  border-radius: 7px;
  font-size: 0.98rem;

  &.is-info {
    background: var(--va-info-soft);
    color: var(--va-info);
  }

  &.is-primary {
    background: var(--va-primary-soft);
    color: var(--va-primary);
  }

  &.is-neutral {
    background: var(--va-surface-subtle);
    color: var(--va-text-muted);
  }
}

@media (max-width: 980px) {
  .dashboard-summary,
  .dashboard-summary.is-novice {
    grid-template-columns: 1fr 1fr;
  }

  .dashboard-shortcuts {
    grid-column: 1 / -1;
  }
}

@media (max-width: 580px) {
  .dashboard-summary,
  .dashboard-summary.is-novice {
    grid-template-columns: 1fr;
  }

  .dashboard-shortcuts {
    grid-column: auto;
  }
}
</style>
