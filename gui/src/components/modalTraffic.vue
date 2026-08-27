<template>
  <div class="modal-card traffic-modal">
    <header class="modal-card-head">
      <p class="modal-card-title">{{ $t("traffic.title") }}</p>
    </header>
    <section class="modal-card-body">
      <div class="traffic-summary">
        <div class="traffic-summary-card is-upload">
          <div class="traffic-summary-row traffic-summary-labels">
            <div class="traffic-summary-label">
              <i class="mdi mdi-arrow-up" aria-hidden="true"></i>
              {{ $t("traffic.upload") }}
            </div>
            <div class="traffic-summary-label traffic-summary-total-label">
              {{ $t("traffic.total") }}
            </div>
          </div>
          <div class="traffic-summary-row traffic-summary-values">
            <div>{{ formatRate(traffic.up) }}</div>
            <div>{{ formatBytes(traffic.upTotal) }}</div>
          </div>
        </div>
        <div class="traffic-summary-card is-download">
          <div class="traffic-summary-row traffic-summary-labels">
            <div class="traffic-summary-label">
              <i class="mdi mdi-arrow-down" aria-hidden="true"></i>
              {{ $t("traffic.download") }}
            </div>
            <div class="traffic-summary-label traffic-summary-total-label">
              {{ $t("traffic.total") }}
            </div>
          </div>
          <div class="traffic-summary-row traffic-summary-values">
            <div>{{ formatRate(traffic.down) }}</div>
            <div>{{ formatBytes(traffic.downTotal) }}</div>
          </div>
        </div>
      </div>

      <div class="traffic-chart-panel">
        <div class="traffic-chart-heading">
          <span>{{ $t("traffic.realtime") }}</span>
          <span class="traffic-live">
            <span class="traffic-live-dot"></span>
            {{ $t("traffic.live") }}
          </span>
        </div>
        <TrafficChart :samples="traffic.samples" />
      </div>
      <p class="traffic-note">{{ $t("traffic.note") }}</p>
    </section>
  </div>
</template>

<script>
import TrafficChart from "@/components/trafficChart";

export default {
  name: "ModalTraffic",
  components: { TrafficChart },
  props: {
    traffic: {
      type: Object,
      required: true,
    },
  },
  methods: {
    formatBytes(bytes) {
      return this.formatValue(bytes, false);
    },
    formatRate(bytes) {
      return this.formatValue(bytes, true);
    },
    formatValue(bytes, perSecond) {
      const value = Math.max(0, Number(bytes) || 0);
      const units = ["B", "KB", "MB", "GB", "TB"];
      let unit = 0;
      let scaled = value;
      while (scaled >= 1024 && unit < units.length - 1) {
        scaled /= 1024;
        unit++;
      }
      const digits = scaled >= 100 || unit === 0 ? 0 : scaled >= 10 ? 1 : 2;
      return `${scaled.toFixed(digits)} ${units[unit]}${perSecond ? "/s" : ""}`;
    },
  },
};
</script>

<style lang="scss" scoped>
.traffic-modal {
  width: min(860px, calc(100vw - 2rem));
  margin: auto;
}

.modal-card-body {
  overflow-x: hidden;
}

.traffic-summary {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 1rem;
  margin-bottom: 1rem;
}

.traffic-summary-card,
.traffic-chart-panel {
  border: 1px solid rgba(127, 127, 127, 0.22);
  border-radius: 10px;
  background: rgba(127, 127, 127, 0.055);
}

.traffic-summary-card {
  display: block;
  padding: 1rem 1.1rem;
  border-left-width: 4px;
}

.traffic-summary-card.is-upload {
  border-left-color: #00b894;
}

.traffic-summary-card.is-download {
  border-left-color: #3273dc;
}

.traffic-summary-label {
  color: #7a7a7a;
  font-size: 0.88rem;
}

.traffic-summary-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 0.8fr);
  align-items: end;
  column-gap: 1rem;
}

.traffic-summary-row > :last-child {
  text-align: right;
}

.traffic-summary-labels {
  min-height: 1.4rem;
}

.traffic-summary-total-label {
  white-space: nowrap;
}

.traffic-summary-card.is-upload .traffic-summary-label i {
  color: #00b894;
}

.traffic-summary-card.is-download .traffic-summary-label i {
  color: #3273dc;
}

.traffic-summary-values {
  margin-top: 0.35rem;
  font-size: clamp(1.25rem, 2.6vw, 1.75rem);
  font-weight: 600;
  line-height: 1.2;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.traffic-chart-panel {
  padding: 0.9rem 1rem 0.8rem;
}

.traffic-chart-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-weight: 600;
}

.traffic-live {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  color: #7a7a7a;
  font-size: 0.76rem;
  font-weight: 400;
}

.traffic-live-dot {
  width: 0.48rem;
  height: 0.48rem;
  border-radius: 50%;
  background: #00b894;
  box-shadow: 0 0 0 0 rgba(0, 184, 148, 0.45);
  animation: traffic-pulse 1.8s infinite;
}

.traffic-note {
  margin-top: 0.75rem;
  color: #7a7a7a;
  font-size: 0.78rem;
  text-align: center;
}

@keyframes traffic-pulse {
  70% {
    box-shadow: 0 0 0 7px rgba(0, 184, 148, 0);
  }
  100% {
    box-shadow: 0 0 0 0 rgba(0, 184, 148, 0);
  }
}

@media (max-width: 600px) {
  .traffic-summary {
    grid-template-columns: 1fr;
    gap: 0.65rem;
  }

  .traffic-summary-values {
    font-size: clamp(1.35rem, 7vw, 1.75rem);
  }

  .traffic-chart-panel {
    padding-left: 0.45rem;
    padding-right: 0.45rem;
  }
}
</style>
