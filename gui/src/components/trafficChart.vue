<template>
  <div class="traffic-chart">
    <div class="traffic-chart-legend">
      <button
        type="button"
        class="traffic-legend-item"
        :class="{ muted: !showUp }"
        :aria-pressed="showUp ? 'true' : 'false'"
        @click="showUp = !showUp"
      >
        <span class="legend-dot is-upload"></span>
        {{ $t("traffic.upload") }}
      </button>
      <button
        type="button"
        class="traffic-legend-item"
        :class="{ muted: !showDown }"
        :aria-pressed="showDown ? 'true' : 'false'"
        @click="showDown = !showDown"
      >
        <span class="legend-dot is-download"></span>
        {{ $t("traffic.download") }}
      </button>
    </div>

    <svg
      class="traffic-svg"
      viewBox="0 0 760 280"
      role="img"
      :aria-label="$t('traffic.chartLabel')"
    >
      <defs>
        <linearGradient id="traffic-upload-area" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stop-color="#00b894" stop-opacity="0.34" />
          <stop offset="100%" stop-color="#00b894" stop-opacity="0.03" />
        </linearGradient>
        <linearGradient id="traffic-download-area" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stop-color="#3273dc" stop-opacity="0.34" />
          <stop offset="100%" stop-color="#3273dc" stop-opacity="0.03" />
        </linearGradient>
      </defs>

      <g class="chart-grid">
        <g v-for="tick in ticks" :key="tick.y">
          <line :x1="plotLeft" :x2="plotRight" :y1="tick.y" :y2="tick.y" />
          <text x="8" :y="tick.y + 4">{{ formatRate(tick.value) }}</text>
        </g>
        <line
          v-for="column in verticalGrid"
          :key="column"
          :x1="column"
          :x2="column"
          :y1="plotTop"
          :y2="plotBottom"
        />
      </g>

      <polygon
        v-if="showDown && downPoints"
        :points="areaPoints(downPoints)"
        fill="url(#traffic-download-area)"
      />
      <polygon
        v-if="showUp && upPoints"
        :points="areaPoints(upPoints)"
        fill="url(#traffic-upload-area)"
      />
      <polyline
        v-if="showDown"
        :points="downPoints"
        class="traffic-line is-download"
      />
      <polyline
        v-if="showUp"
        :points="upPoints"
        class="traffic-line is-upload"
      />
    </svg>

    <div class="traffic-time-axis">
      <span>{{ $t("traffic.secondsAgo", { seconds: 60 }) }}</span>
      <span>{{ $t("traffic.now") }}</span>
    </div>
  </div>
</template>

<script>
export default {
  name: "TrafficChart",
  props: {
    samples: {
      type: Array,
      default: () => [],
    },
  },
  data: () => ({
    showUp: true,
    showDown: true,
    plotLeft: 88,
    plotRight: 746,
    plotTop: 14,
    plotBottom: 254,
  }),
  computed: {
    visibleSamples() {
      return this.samples.slice(-60);
    },
    maxValue() {
      let max = 1024;
      for (const sample of this.visibleSamples) {
        if (this.showUp) max = Math.max(max, Number(sample.up) || 0);
        if (this.showDown) max = Math.max(max, Number(sample.down) || 0);
      }
      return this.niceMaximum(max);
    },
    ticks() {
      return [0, 1, 2, 3, 4].map((index) => ({
        value: (this.maxValue * (4 - index)) / 4,
        y: this.plotTop + ((this.plotBottom - this.plotTop) * index) / 4,
      }));
    },
    verticalGrid() {
      return [0, 1, 2, 3, 4].map(
        (index) =>
          this.plotLeft + ((this.plotRight - this.plotLeft) * index) / 4
      );
    },
    upPoints() {
      return this.seriesPoints("up");
    },
    downPoints() {
      return this.seriesPoints("down");
    },
  },
  methods: {
    niceMaximum(value) {
      if (value <= 1024) return 1024;
      const power = Math.pow(10, Math.floor(Math.log10(value)));
      const normalized = value / power;
      const nice = normalized <= 2 ? 2 : normalized <= 5 ? 5 : 10;
      return nice * power;
    },
    seriesPoints(field) {
      const samples = this.visibleSamples;
      if (!samples.length) return "";
      const width = this.plotRight - this.plotLeft;
      const height = this.plotBottom - this.plotTop;
      const offset = 60 - samples.length;
      return samples
        .map((sample, index) => {
          const x = this.plotLeft + (width * (offset + index)) / 59;
          const value = Math.max(0, Number(sample[field]) || 0);
          const y =
            this.plotBottom - Math.min(value / this.maxValue, 1) * height;
          return `${x.toFixed(2)},${y.toFixed(2)}`;
        })
        .join(" ");
    },
    areaPoints(points) {
      if (!points) return "";
      const entries = points.split(" ");
      const firstX = entries[0].split(",")[0];
      const lastX = entries[entries.length - 1].split(",")[0];
      return `${firstX},${this.plotBottom} ${points} ${lastX},${this.plotBottom}`;
    },
    formatRate(bytes) {
      const value = Number(bytes) || 0;
      if (value < 1024) return `${Math.round(value)} B/s`;
      if (value < 1024 * 1024)
        return `${(value / 1024).toFixed(value < 10 * 1024 ? 1 : 0)} KB/s`;
      if (value < 1024 * 1024 * 1024)
        return `${(value / 1024 / 1024).toFixed(
          value < 10 * 1024 * 1024 ? 1 : 0
        )} MB/s`;
      return `${(value / 1024 / 1024 / 1024).toFixed(1)} GB/s`;
    },
  },
};
</script>

<style lang="scss" scoped>
.traffic-chart {
  width: 100%;
}

.traffic-chart-legend {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
  margin-bottom: 0.35rem;
}

.traffic-legend-item {
  appearance: none;
  border: 0;
  border-radius: 999px;
  background: transparent;
  color: inherit;
  cursor: pointer;
  padding: 0.25rem 0.55rem;
  font: inherit;
  font-size: 0.82rem;
}

.traffic-legend-item:hover,
.traffic-legend-item:focus-visible {
  background: rgba(127, 127, 127, 0.12);
}

.traffic-legend-item.muted {
  opacity: 0.38;
}

.legend-dot {
  display: inline-block;
  width: 0.62rem;
  height: 0.62rem;
  margin-right: 0.28rem;
  border-radius: 50%;
}

.legend-dot.is-upload {
  background: #00b894;
}

.legend-dot.is-download {
  background: #3273dc;
}

.traffic-svg {
  display: block;
  width: 100%;
  min-height: 220px;
  overflow: visible;
}

.chart-grid line {
  stroke: currentColor;
  stroke-width: 1;
  opacity: 0.1;
}

.chart-grid text {
  fill: currentColor;
  font-size: 11px;
  opacity: 0.62;
}

.traffic-line {
  fill: none;
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 2.5;
  vector-effect: non-scaling-stroke;
}

.traffic-line.is-upload {
  stroke: #00b894;
}

.traffic-line.is-download {
  stroke: #3273dc;
}

.traffic-time-axis {
  display: flex;
  justify-content: space-between;
  padding-left: 5.5rem;
  color: #7a7a7a;
  font-size: 0.75rem;
}

@media (max-width: 600px) {
  .traffic-svg {
    min-height: 180px;
  }

  .traffic-time-axis {
    padding-left: 4.25rem;
  }
}
</style>
