<template>
  <div class="modal-card" style="max-width: 500px; margin: auto">
    <header class="modal-card-head">
      <p class="modal-card-title has-text-centered">{{ title }}</p>
    </header>
    <section class="modal-card-body lazy" style="text-align: center">
      <div><canvas ref="canvas" class="qrcode"></canvas></div>
      <div
        class="tags has-addons is-centered sharing-tags"
        @mouseenter="showCopyCover = true"
        @mouseleave="showCopyCover = false"
      >
        <span
          class="tag is-rounded is-dark sharingAddressTag"
          style="position: relative"
          :data-clipboard-text="sharingAddress"
        >
          <span v-show="showCopyCover" class="tag-cover tag is-rounded"></span>
          <span class="has-ellipsis" style="max-width: 10em">
            {{ shortDesc }}
          </span>
        </span>
        <span v-show="showCopyCover" class="tag-cover-text">{{ $t("operations.copyLink") }}</span>
        <span
          class="tag is-rounded is-primary sharingAddressTag"
          style="position: relative"
          :data-clipboard-text="sharingAddress"
        >
          <span class="has-ellipsis" style="max-width: 25em">
            {{ sharingAddress }}
          </span>
          <span v-show="showCopyCover" class="tag-cover tag is-rounded"></span>
        </span>
      </div>
    </section>

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
        type: "is-primary",
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
.modal-card-head,
.modal-card-foot {
  border-radius: 0.25rem;
}
.modal-card-head {
  border-bottom-left-radius: 0;
  border-bottom-right-radius: 0;
}
.modal-card-foot {
  border-top-left-radius: 0;
  border-top-right-radius: 0;
}

.qrcode {
  min-height: 300px;
  min-width: 300px;
  max-width: 100%;
}

.sharing-tags {
  position: relative;
}

.tag-cover {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  background-color: rgba(0, 0, 0, 0.6) !important;
  pointer-events: none;
}

.tag-cover-text {
  position: absolute;
  inset: 0;
  z-index: 2;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #fff;
  pointer-events: none;
}

@media screen and (max-width: 480px) {
  .qrcode {
    min-height: 0;
    min-width: 0;
    width: 100% !important;
    height: auto !important;
  }
}
</style>
