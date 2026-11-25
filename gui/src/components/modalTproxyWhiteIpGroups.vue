<template>
    <div class="modal-card" style="max-width: 480px; width: calc(100vw - 2rem); margin: auto">
        <header class="modal-card-head">
            <p class="modal-card-title">
                {{ $t("tproxyWhiteIpGroups.title") }}
            </p>
            <button
                type="button"
                class="delete"
                :aria-label="$t('operations.close')"
                @click="$emit('close')"
            ></button>
        </header>
        <section class="modal-card-body">
            <b-message type="is-info" class="after-line-dot5">
                <p>{{ $t("tproxyWhiteIpGroups.messages.0") }}</p>
            </b-message>
            <b-field :label="$t('tproxyWhiteIpGroups.title')">
                <div class="va-chip-group" role="group" :aria-label="$t('tproxyWhiteIpGroups.title')">
                    <button
                        v-for="group in ipGroupOptions"
                        :key="group.value"
                        type="button"
                        class="va-chip"
                        :class="{ 'is-selected': countryCodes.includes(group.value) }"
                        :aria-pressed="String(countryCodes.includes(group.value))"
                        @click="toggleCountryCode(group.value)"
                    >
                        <i
                            v-if="countryCodes.includes(group.value)"
                            class="mdi mdi-check"
                            aria-hidden="true"
                        ></i>
                        {{ $t(group.labelKey) }}
                    </button>
                </div>
            </b-field>
            <b-field :label="$t('tproxyWhiteIpGroups.formName2')">
                <b-input v-model="customIps" type="textarea" :placeholder="$t('tproxyWhiteIpGroups.formPlaceholder2')"
                    custom-class="full-min-height horizon-scroll code-font" />
            </b-field>
            <b-message type="is-warning" class="after-line-dot5">
                <p>{{ $t("tproxyWhiteIpGroups.messages.1") }}</p>
            </b-message>
        </section>
        <footer class="modal-card-foot flex-end">
            <button class="button" @click="$emit('close')">
                {{ $t("operations.cancel") }}
            </button>
            <button class="button is-primary" @click="handleClickSubmit">
                {{ $t("operations.save") }}
            </button>
        </footer>
    </div>
</template>
<script>
import { handleResponse } from "@/assets/js/utils";

export default {
    name: "modalTproxyWhiteIpGroups",
    data: () => ({
        ipGroupOptions: [
            { value: "CN", labelKey: "tproxyWhiteIpGroups.cn" },
            { value: "PRIVATE", labelKey: "tproxyWhiteIpGroups.private" },
            { value: "US", labelKey: "tproxyWhiteIpGroups.us" },
            { value: "CLOUDFLARE", labelKey: "tproxyWhiteIpGroups.cloudflare" },
        ],
        countryCodes: [],
        customIps: "",
    }),
    created() {
        this.$axios({
            url: apiRoot + "/tproxyWhiteIpGroups",
        }).then((res) => {
            handleResponse(res, this, () => {
                if (res.data.data.countryCodes) {
                    this.countryCodes = res.data.data.countryCodes.filter(code => code !== "NONE");
                }
                if (res.data.data.customIps) {
                    this.customIps = res.data.data.customIps.join("\n");
                }
            });
        });
    },
    methods: {
        toggleCountryCode(code) {
            const idx = this.countryCodes.indexOf(code);
            if (idx >= 0) {
                this.countryCodes.splice(idx, 1);
            } else {
                this.countryCodes.push(code);
            }
        },
        validateCIDRArray(arr) {
            const ipv4Cidr = /^(?:25[0-5]|2[0-4]\d|1\d{2}|[1-9]?\d)(?:\.(?:25[0-5]|2[0-4]\d|1\d{2}|[1-9]?\d)){3}\/(?:[0-9]|[12]\d|3[0-2])$/;

            const ipv6Cidr = /^(?:(?:[A-Fa-f0-9]{1,4}:){7}[A-Fa-f0-9]{1,4}|(?:[A-Fa-f0-9]{1,4}:){1,7}:|(?:[A-Fa-f0-9]{1,4}:){1,6}:[A-Fa-f0-9]{1,4}|(?:[A-Fa-f0-9]{1,4}:){1,5}(?::[A-Fa-f0-9]{1,4}){1,2}|(?:[A-Fa-f0-9]{1,4}:){1,4}(?::[A-Fa-f0-9]{1,4}){1,3}|(?:[A-Fa-f0-9]{1,4}:){1,3}(?::[A-Fa-f0-9]{1,4}){1,4}|(?:[A-Fa-f0-9]{1,4}:){1,2}(?::[A-Fa-f0-9]{1,4}){1,5}|[A-Fa-f0-9]{1,4}:(?:(?::[A-Fa-f0-9]{1,4}){1,6})|:(?:(?::[A-Fa-f0-9]{1,4}){1,7}|:))\/(?:12[0-8]|1[01]\d|[1-9]?\d)$/;

            const invalid = arr
                .map(s => (typeof s === 'string' ? s.trim() : ''))
                .filter(s => s.length === 0 || !(ipv4Cidr.test(s) || ipv6Cidr.test(s)));
            return invalid.length === 0;
        },
        handleClickSubmit() {
            const customIps = this.customIps.split("\n").map(line => line.trim()).filter(Boolean);
            if (!this.validateCIDRArray(customIps)) {
                this.$buefy.toast.open({
                    message: this.$t("tproxyWhiteIpGroups.invalidCustomIps"),
                    type: "is-danger",
                    position: "is-top",
                    queue: false,
                    duration: 10000,
                });
                return
            }
            this.$axios({
                url: apiRoot + "/tproxyWhiteIpGroups",
                method: "put",
                data: {
                    countryCodes: this.countryCodes.length ? this.countryCodes : ['NONE'],
                    customIps,
                },
            }).then((res) => {
                handleResponse(res, this, () => {
                    this.$emit("close");
                });
            });
        },
    },
};
</script>
