<template>
  <div class="modal-card sharing-modal">
    <header class="modal-card-head">
      <p class="modal-card-title">{{ title }}</p>
      <button
        type="button"
        class="delete"
        :aria-label="$t('operations.close')"
        @click="$parent.close()"
      ></button>
    </header>
    <section class="modal-card-body lazy" style="text-align: center">
      <div class="qrcode-frame">
        <canvas ref="canvas" class="qrcode"></canvas>
      </div>
      <div class="sharing-desc">{{ shortDesc }}</div>
      <div class="sharing-link-row">
        <code class="sharing-link-text" :title="sharingAddress">{{ sharingAddress }}</code>
        <button
          type="button"
          class="button is-primary is-small sharingAddressTag"
          :data-clipboard-text="sharingAddress"
        >
          <i class="mdi mdi-content-copy" style="margin-right: 4px" aria-hidden="true"></i>
          {{ $t("operations.copyLink") }}
        </button>
      </div>
    </section>
    <footer class="modal-card-foot flex-end">
      <b-button @click="$parent.close()">{{ $t("operations.close") }}</b-button>
    </footer>
  </div>
</template>

<script>
import QRCode from "qrcode";
import ClipboardJS from "clipboard";
import CONST from "@/assets/js/const";
import { Base64 } from "js-base64";
import i18n from "@/plugins/i18n";

export default {
  name: "ModalSharing",
  i18n,
  props: {
    title: {
      type: String,
      required: true,
    },
    sharingAddress: {
      type: String,
      required: true,
    },
    shortDesc: {
      type: String,
      required: true,
    },
    type: {
      type: String,
      required: true,
    },
  },
  data: () => ({
    clipboard: null,
    showCopyCover: false,
  }),
  beforeDestroy() {
    if (this.clipboard) this.clipboard.destroy();
  },
  mounted() {
    this.clipboard = new ClipboardJS(
      this.$el.querySelectorAll(".sharingAddressTag")
    );
    this.clipboard.on("success", (e) => {
      this.$buefy.toast.open({
        message: this.$t("common.success"),
        type: "is-success",
        position: "is-top",
        queue: false,
      });
      e.clearSelection();
    });
    this.clipboard.on("error", (e) => {
      this.$buefy.toast.open({
        message: this.$t("common.fail") + ", error:" + e.toLocaleString(),
        type: "is-warning",
        position: "is-top",
        queue: false,
      });
    });

    let add = this.sharingAddress;
    if (this.type === CONST.SubscriptionType) {
      add = "sub://" + Base64.encode(add);
    }
    QRCode.toCanvas(
      this.$refs.canvas,
      add,
      { errorCorrectionLevel: "H" },
      function (error) {
        if (error) console.error(error);
        // console.log("QRCode has been generated successfully!");
      }
    );
  },
};
</script>

<style scoped>
.sharing-modal {
  max-width: 500px;
  width: calc(100vw - 2rem);
  margin: auto;
}

.qrcode-frame {
  display: inline-flex;
  padding: 12px;
  border-radius: 12px;
  background: #ffffff;
  border: 1px solid var(--va-border);
  box-shadow: var(--va-shadow-sm);
  margin-bottom: 0.75rem;
}

.qrcode {
  display: block;
  min-height: 240px;
  min-width: 240px;
  max-width: 100%;
}

.sharing-desc {
  font-weight: 600;
  color: var(--va-text);
  margin-bottom: 0.75rem;
  word-break: break-word;
}

.sharing-link-row {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.45rem 0.55rem;
  border-radius: var(--va-radius-sm);
  background: var(--va-surface-subtle);
  border: 1px solid var(--va-border);
  text-align: left;
}

.sharing-link-text {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.8rem;
  background: transparent !important;
  padding: 0;
}

@media screen and (max-width: 480px) {
  .qrcode {
    min-height: 0;
    min-width: 0;
    width: 100% !important;
    height: auto !important;
  }

  .sharing-link-row {
    flex-direction: column;
    align-items: stretch;
  }
}
</style>
