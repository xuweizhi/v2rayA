<template>
  <div class="modal-card" style="max-width: 760px; margin: auto">
    <header class="modal-card-head">
      <p class="modal-card-title">
        {{ $t("connections.title") }}
        <span style="font-size: 0.75em; color: #888">
          ({{ connections.length }})
        </span>
      </p>
    </header>
    <section class="modal-card-body">
      <div class="conn-toolbar">
        <b-button size="is-small" @click="paused = !paused">
          {{ paused ? $t("connections.resume") : $t("connections.pause") }}
        </b-button>
        <b-button size="is-small" @click="connections = []">
          {{ $t("connections.clear") }}
        </b-button>
        <span class="conn-note">{{ $t("connections.note") }}</span>
      </div>
      <b-table
        :data="connections"
        :per-page="200"
        :current-page.sync="page"
        hoverable
        style="max-height: 420px"
      >
        <b-table-column field="time" :label="$t('connections.time')" width="90" v-slot="p">
          {{ formatTime(p.row.time) }}
        </b-table-column>
        <b-table-column field="source" :label="$t('connections.source')" width="200" v-slot="p">
          <span class="code-font">{{ p.row.source }}</span>
        </b-table-column>
        <b-table-column field="dest" :label="$t('connections.dest')" v-slot="p">
          <span class="code-font dest-cell" :title="$t('connections.copyDest')" @click="copyText(p.row.dest)">
            {{ p.row.dest }}
          </span>
        </b-table-column>
        <b-table-column field="outbound" :label="$t('connections.outbound')" width="170" v-slot="p">
          {{ p.row.outbound }}
        </b-table-column>
        <b-table-column field="inbound" :label="$t('connections.inbound')" width="110" v-slot="p">
          {{ p.row.inbound }}
        </b-table-column>
      </b-table>
    </section>
  </div>
</template>

<script>
import { handleResponse } from "@/assets/js/utils";
export default {
  name: "ModalConnections",
  data: () => ({
    connections: [],
    paused: false,
    page: 1,
    timer: null,
  }),
  created() {
    this.refresh();
    this.timer = setInterval(() => {
      if (!this.paused) this.refresh();
    }, 3000);
  },
  beforeDestroy() {
    if (this.timer) clearInterval(this.timer);
  },
  methods: {
    refresh() {
      this.$axios({
        url: apiRoot + "/connections?limit=500",
        method: "get",
      }).then((res) => {
        if (res.data && res.data.code === "SUCCESS" && res.data.data) {
          this.connections = res.data.data.connections || [];
        }
      });
    },
    formatTime(t) {
      const d = new Date(t);
      const pad = (n) => String(n).padStart(2, "0");
      return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
    },
    copyText(text) {
      if (!text) return;
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(text).then(() => {
          this.$buefy.toast.open({
            message: this.$t("common.success"),
            type: "is-primary",
            position: "is-top",
            duration: 1500,
            queue: false,
          });
        });
      }
    },
  },
};
</script>
