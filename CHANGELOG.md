# Changelog

All notable changes to synthkit. Generated from Conventional Commits.

## [1.4.0](https://github.com/rknightion/synthkit/compare/v1.3.1...v1.4.0) (2026-09-05)


### Features

* add canonical telemetry inventory ([a776e4f](https://github.com/rknightion/synthkit/commit/a776e4fa3ad9ce8179b5c5593529ca3075320cef))
* add declarable network topology churn ([10197de](https://github.com/rknightion/synthkit/commit/10197de66b924f4b469c23dafe1445d170a5d37e))
* add EKS live reality readback ([501ee64](https://github.com/rknightion/synthkit/commit/501ee647db0f9f04c2186827ab254caada05d908))
* add k3d signal fidelity capture lab ([fca0503](https://github.com/rknightion/synthkit/commit/fca05038f3d6b2fb95b3b097181b9a4f713731e2))
* add native k8s OTLP and high-DPM cadence ([44a2c3d](https://github.com/rknightion/synthkit/commit/44a2c3de9045f6ad43ebdf1377abae0c86bd5bfe))
* add report-only signal fidelity corpus gate ([fd5aba2](https://github.com/rknightion/synthkit/commit/fd5aba271177160bbe87c8618880e2f86072ac19))
* capture the OTel-native receiver permutation and make the corpus permutation-aware ([1f3b433](https://github.com/rknightion/synthkit/commit/1f3b433ec28f844d242338ae1d407d385326871e))
* carry explicit metric producer provenance ([f27b374](https://github.com/rknightion/synthkit/commit/f27b374137ceb661bafb6429521ea78e9d88580e))
* **chart:** add a values schema, and make the manifest validation leg actually run ([4e478ec](https://github.com/rknightion/synthkit/commit/4e478ecb7a98b89c5178cfc1ad8c19e0de7d36f8))
* close CloudWatch coverage gaps safely ([ada2f9d](https://github.com/rknightion/synthkit/commit/ada2f9d9ba8c359615158fda6ac64e2fd1b55868))
* **fidelity:** report the measured readability bound ([3739b39](https://github.com/rknightion/synthkit/commit/3739b391d1a3e9cb6f294d57557c55279517fd0b))
* **lab:** capture the OTel Prometheus-exporter permutation ([f7c266a](https://github.com/rknightion/synthkit/commit/f7c266a590e5963995be9d7e0588f9c24b73cd4a))
* make the signal-fidelity gate trustworthy, and close SKT-0004/0006.05/0007.01/0007.02/0008 ([b7ddb63](https://github.com/rknightion/synthkit/commit/b7ddb63c2e6cdf8834f1590eadd5954ee531bec0))
* model captured telemetry and refresh control UI ([b135d84](https://github.com/rknightion/synthkit/commit/b135d84e64bd7724e30eecbe573f7d1db70cdd59))
* model prometheus operator envelope ([2279ff7](https://github.com/rknightion/synthkit/commit/2279ff7eee71af0c4f12d281bb93046e6d2556d9))
* **otlp:** integrate captured Beyla and CloudWatch native contracts ([9abdd42](https://github.com/rknightion/synthkit/commit/9abdd42425117ad241ed37648eff33690a2ab803))
* restore gate trust and broaden native OTLP ([9263c7f](https://github.com/rknightion/synthkit/commit/9263c7f0849a64002eae895d6fbf904b1e47a714))
* retain partial corpus promotion evidence ([73a13f4](https://github.com/rknightion/synthkit/commit/73a13f426a56eb64140db8cdfc63a3bcac110f57))
* ship high-DPM detector blueprint ([6e74d7e](https://github.com/rknightion/synthkit/commit/6e74d7ee62d8c3a02209de5c4bb093b069843440))
* ship the Helm chart, fix le rendering catalogue-wide, make pod logs comparable ([c705977](https://github.com/rknightion/synthkit/commit/c7059771136e2344d768eb202e815f5010a4f530))
* turn the k3d lab into a permutation matrix, and fix the Loki decode it exposed ([e534c58](https://github.com/rknightion/synthkit/commit/e534c58284a4a4532b651466b34669790a899b7e))


### Bug Fixes

* activate reviewed reality corpus ([8dc5d9b](https://github.com/rknightion/synthkit/commit/8dc5d9b078c1c201bfff4c915ba3ef0a54288c32))
* align e2e dump and receiver inventories ([25e8c04](https://github.com/rknightion/synthkit/commit/25e8c040967b9fb1793afba399bdcaca99f24929))
* align span metrics with trace outcomes ([30db2ba](https://github.com/rknightion/synthkit/commit/30db2baf71c9e4d03d5c2459806be677dca72aeb))
* **backlog:** correct SKT-0016 — the e2e failure is a subset-correlation regression ([23cf077](https://github.com/rknightion/synthkit/commit/23cf0772884a5b3a8cc46c54df7c354046209ab2))
* **backlog:** correct SKT-0018 — the live scheduler does reach the OTLP-logs lane ([894206b](https://github.com/rknightion/synthkit/commit/894206b322ca23702c31c3bd48a814dc9c445598))
* **blueprints:** the agent fleet in otlp-native was a test fixture, not a workload ([187b7f0](https://github.com/rknightion/synthkit/commit/187b7f030890a3f25d4b04e8a5a28850a3b6549a))
* **capture:** recognize Karpenter providers and platform products ([a5763e4](https://github.com/rknightion/synthkit/commit/a5763e4a799741b25f6d2c955bae79b6ce596c11))
* **control:** derive readiness from active feeds ([0007368](https://github.com/rknightion/synthkit/commit/0007368994e18c1d3581cf4bfe7b25a29473f8e2))
* **cw:** derive the Summary sum from average and count, surface skipped bases in inventory ([ecfd282](https://github.com/rknightion/synthkit/commit/ecfd282f031c26e05e2e738f65005967385fea61))
* declare per-family query identities ([401bc6a](https://github.com/rknightion/synthkit/commit/401bc6afc78da98cc837fae3c9f26719862c8687))
* **deps:** update module filippo.io/age to v1.3.2 ([#122](https://github.com/rknightion/synthkit/issues/122)) ([9f680f6](https://github.com/rknightion/synthkit/commit/9f680f6018573d96105e19a48a85bf4827cfa28b))
* **deps:** update module github.com/grafana/nanogit to v1.4.2 ([#105](https://github.com/rknightion/synthkit/issues/105)) ([fb52659](https://github.com/rknightion/synthkit/commit/fb526597cfe44dddef630f43eda610ef45908c5d))
* **deps:** update module github.com/moby/moby/api to v1.56.0 ([#134](https://github.com/rknightion/synthkit/issues/134)) ([2f06835](https://github.com/rknightion/synthkit/commit/2f06835a4913aec6eb8cfa63c00a20a7cca87424))
* **e2e:** gate the published-image sigil assertions on the agent fixture ([0bc1828](https://github.com/rknightion/synthkit/commit/0bc1828770fc3e4f84cd8d0061ddda86d1679656))
* **e2e:** wait on a real HTTP answer before polling readiness ([9d8aec5](https://github.com/rknightion/synthkit/commit/9d8aec52e7288f450e085019223863eb1bdd6c3e))
* **fidelity:** isolate folded source coverage ([a5d194b](https://github.com/rknightion/synthkit/commit/a5d194b3ea599052f88fc08c5a62c13ff947e22d))
* **fidelity:** resolve surviving contradiction exemptions ([34bb8d3](https://github.com/rknightion/synthkit/commit/34bb8d32f44b1eb454da5fc14a30824e0a04aff8))
* **inventory:** classify open label values as gaps ([0c91077](https://github.com/rknightion/synthkit/commit/0c9107703fe94aa698eb57030713633d3917164b))
* **inventory:** classify text-dump logs before combining streams ([993688d](https://github.com/rknightion/synthkit/commit/993688d3dc4cb1f74f3cd6fec244fb746486f7c7))
* **inventory:** ignore cross-producer label unions ([be71087](https://github.com/rknightion/synthkit/commit/be71087c7935d26ded5da6f83598861dee3f9b51))
* **inventory:** preserve Prometheus summary kind ([35d11c4](https://github.com/rknightion/synthkit/commit/35d11c448f5c61e68f4c1b898f543bb138b5c24f))
* make skcapture permissions truthful ([05cae3b](https://github.com/rknightion/synthkit/commit/05cae3b776612ffc7bb69f3434e394c261e02349))
* **notices:** use the /v2 module path go-licenses requires ([fc3b87e](https://github.com/rknightion/synthkit/commit/fc3b87ee72e8a30dcca0af8db4bc8a656b380268))
* pin available artifact action release ([9929062](https://github.com/rknightion/synthkit/commit/99290621af3622475152cf3938ff9395a8f0a8c6))
* reject silent runtime identities ([70208b9](https://github.com/rknightion/synthkit/commit/70208b9b1a9e7fc495c8908626335ad578a05820))
* remove forbidden stack name from SKT-0006 description ([67a8a9b](https://github.com/rknightion/synthkit/commit/67a8a9b93f3256dae8fa55adccc9904932e09f55))
* **runner:** activate high-DPM cadence ([415f0d6](https://github.com/rknightion/synthkit/commit/415f0d6f3026835f823f6fd986d429fa77219b1e))
* **signal-fidelity:** report before exemption drift failure ([90eaeda](https://github.com/rknightion/synthkit/commit/90eaedaa0c35579588b5fd2ca422e06c8cf1da28))
* stop the capture recording inferred evidence, and re-examine every prior capture ([4fa184e](https://github.com/rknightion/synthkit/commit/4fa184e606ea195932ad832d1a683807d0fce47a))
* use portable conformance audit command ([863dec9](https://github.com/rknightion/synthkit/commit/863dec936bf36d38472d9b466d7e4013f2a9afbb))


### Refactor

* **cw:** split the Metric Streams lookup table per namespace group ([c4ae611](https://github.com/rknightion/synthkit/commit/c4ae61150312ed7d68a1a65b036dc779b2b5bcbe))


### Documentation

* **acceptance:** record fresh-stack user round ([211b817](https://github.com/rknightion/synthkit/commit/211b817bc537972d6f98927ea1b5c1d413fc2f4e))
* **backlog:** close dump correlation repair with hosted proof ([1354975](https://github.com/rknightion/synthkit/commit/1354975330e4e9551c5c72e3313a9bd82565e890))
* **backlog:** close SKT-0018 ([1a1e0bf](https://github.com/rknightion/synthkit/commit/1a1e0bf3f3627bd6057accf09146201990a6f967))
* **backlog:** complete Go 1.27 upgrade ([cae239d](https://github.com/rknightion/synthkit/commit/cae239d8f501ae6754c7bc8d067a697b18e3b55f))
* **backlog:** park intermittent e2e failure ([29a6fac](https://github.com/rknightion/synthkit/commit/29a6facf91cec3728764dbb35c7b845a981c897c))
* **backlog:** record SKT-0010.18 exact-SHA CI ([b3affc5](https://github.com/rknightion/synthkit/commit/b3affc5f3681fa56f8101ddff47573f08592c56e))
* **backlog:** record the clean-machine decision for SKT-0011 ([24341e6](https://github.com/rknightion/synthkit/commit/24341e61b972b827aa2c98e206f9875324965d4d))
* **backlog:** sync fan-out protocol — CodeRabbit review gate ([2d56dd6](https://github.com/rknightion/synthkit/commit/2d56dd6034e9d2a973c198001df048d5e27fe2f6))
* **backlog:** sync fan-out protocol — success criteria vs write authority ([5c52fa5](https://github.com/rknightion/synthkit/commit/5c52fa50c732ec4fffb2cd1cde414f290466dd89))
* **backlog:** unblock SKT-0011's stack half ([cacabdd](https://github.com/rknightion/synthkit/commit/cacabdd601cf14865fb5b7bed13843223811a773))
* **captures:** remove a stack name from the Beyla/Envoy OTLP record ([73055a0](https://github.com/rknightion/synthkit/commit/73055a0efd0f39cae34e64fd633162571486a35e))
* classify k8s and host coverage gaps ([4cce6c1](https://github.com/rknightion/synthkit/commit/4cce6c14d06801005b02053f15baacfb2d749964))
* **credentials:** record the read-back credential and the tenant-id trap ([416f8e6](https://github.com/rknightion/synthkit/commit/416f8e6382a23f047433032876928ad9f358c0ee))
* fix reality corpus site links ([2d091b0](https://github.com/rknightion/synthkit/commit/2d091b0388c1ec6bbcccf60631cb3f7dca54141b))
* **k8s:** document monitoring deployment permutations ([96c9490](https://github.com/rknightion/synthkit/commit/96c949012a5a969694e98c7d5a21629694a0b0ef))
* park k8s monitoring evidence gaps ([c852af6](https://github.com/rknightion/synthkit/commit/c852af6fb2286057b536f8530baad4e7a8b1cd52))
* re-import the fan-out protocol at c1e6cb0 ([432f8ee](https://github.com/rknightion/synthkit/commit/432f8ee99f4bb3f3785d5d7ed10418e7e021a622))
* record final parked boundaries ([f45dec2](https://github.com/rknightion/synthkit/commit/f45dec2eda016d89a66f5b5c839d80095feb0d28))
* record lab verification boundaries ([4b7a80a](https://github.com/rknightion/synthkit/commit/4b7a80ae45261b892fe9c45037fceecd8f0971e2))
* record operational readiness closeout ([77c380d](https://github.com/rknightion/synthkit/commit/77c380dfa6c2bec8720529a0171c030beb1cab00))
* resolve SK-75's CloudWatch half and record the observed span resource ([60d99d5](https://github.com/rknightion/synthkit/commit/60d99d5c7f7e0c2853e777cab58afe2dc63a326e))
* scrub deployment host from task notes ([2a4732f](https://github.com/rknightion/synthkit/commit/2a4732f9d8c94ad88f74660ac370d5b964848f5c))
* settle agent observability contract ([2ffcaca](https://github.com/rknightion/synthkit/commit/2ffcacabf6fc316128dacbc861098ca0a759bf36))
* show the runtime control plane in the README ([ceb0b4a](https://github.com/rknightion/synthkit/commit/ceb0b4ab7a82672d8c028d0fe287bf61af1380ea))
* **signals:** capture the Envoy Gateway OTLP surface, close SKT-0008.02 ([0ec5812](https://github.com/rknightion/synthkit/commit/0ec581209493428bc06b6bb7ab7a2a20c73164cb))
* **signals:** capture the full Envoy Gateway OTLP contract ([705f7c0](https://github.com/rknightion/synthkit/commit/705f7c02285bba6cf60f67d37c72ebb9f07cb598))
* **signals:** record the Beyla native ingestion readback ([b46922f](https://github.com/rknightion/synthkit/commit/b46922f809e013ff20df0ff4fe70f4e51c8e79a1))
* **signals:** resolve SK-86, capture the Envoy Gateway control-plane OTLP form ([f4224c1](https://github.com/rknightion/synthkit/commit/f4224c11f611cdf02d06d7bee8084b95c66e5234))
* sync agent-docs, a wave's launch message is a file not a chat block ([f19f4a7](https://github.com/rknightion/synthkit/commit/f19f4a727dcd5b8efa4152a458dc8fb11f5c3410))
* sync Astra routing and default wave reports to files ([f4f31dd](https://github.com/rknightion/synthkit/commit/f4f31dd94ec3cb3f199a7b9c792abcb6e3b06bdc))
* sync nineteen-worker Codex fan-out guidance ([ef184c6](https://github.com/rknightion/synthkit/commit/ef184c672c245c33f9a89afd80b715a0c0d40176))
* sync wave-root stage authority and lab-Mac GUI gate ([1e934c8](https://github.com/rknightion/synthkit/commit/1e934c8d6e466e06c563baf3cab30e23d048d0b2))
* track the k8s-monitoring deployment permutations (SKT-0013) ([bab7692](https://github.com/rknightion/synthkit/commit/bab769273dfb2b507fe70f92cb26c9ec83013246))
* verdict CloudWatch coverage gaps by cause ([0323617](https://github.com/rknightion/synthkit/commit/0323617485326c5c208f3935b486db6bbdce9da3))


### Build & CI

* **auto-rc:** trigger on CI completion instead of push ([13ddcbb](https://github.com/rknightion/synthkit/commit/13ddcbb04dff47bd70592d5208a715b70d22e3de))
* bump the broker-token action pin ([1a70485](https://github.com/rknightion/synthkit/commit/1a70485646c9fded174f9075d533401b67388964))
* **helm:** validate the rendered chart against the Kubernetes API schemas ([5277c1b](https://github.com/rknightion/synthkit/commit/5277c1b0ac0c84b36d4a9b2768f6c75a943d772f))
* install ripgrep for lab validation ([114774d](https://github.com/rknightion/synthkit/commit/114774dfab67cc8e97a27c5672be5b74f1c6778a))
* keep agent fixture out of safety-bound runs ([2eb8725](https://github.com/rknightion/synthkit/commit/2eb872571117b0e892c060779f393c486d8dc8fb))
* repin the shared reusables to v1.18.1 ([9f1a18a](https://github.com/rknightion/synthkit/commit/9f1a18a8baf6f27f9e5c71d1723eff1bd8168e9b))
* **security:** declare least-privilege permissions on every job ([0683180](https://github.com/rknightion/synthkit/commit/0683180cc71e3aa2cbea3a8bfcf42a9ec85ae30b))
* **security:** pin actions to SHAs and stop checkouts persisting credentials ([f834c4b](https://github.com/rknightion/synthkit/commit/f834c4b6fc748a6c5d927947118d2453a7ecac6c))
* tune ci.yml — CI concurrency hygiene ([9ab1e4a](https://github.com/rknightion/synthkit/commit/9ab1e4ae6771d467ee68d8f1ae675c51c927e260))
* tune signal-fidelity-k3d.yml — CI concurrency hygiene ([ffd3b0d](https://github.com/rknightion/synthkit/commit/ffd3b0dc2c60f58cf6c7853774e63b16109effad))
* unblock gitleaks v8.30.1 and guard the allowlist that does it ([4e6ce54](https://github.com/rknightion/synthkit/commit/4e6ce545ec096f75c3aebbfda8ec5d56dea37c15))
* upgrade to Go 1.27 ([0437f2d](https://github.com/rknightion/synthkit/commit/0437f2dfb3a60386b3578926bbb3ec6a5dd44645))

## [1.3.1](https://github.com/rknightion/synthkit/compare/v1.3.0...v1.3.1) (2026-08-21)


### Bug Fixes

* make published state cleanup portable ([bddbc65](https://github.com/rknightion/synthkit/commit/bddbc6574bfded5ab0b0df5612593210e0e02c16))

## [1.3.0](https://github.com/rknightion/synthkit/compare/v1.2.0...v1.3.0) (2026-08-21)


### Features

* complete operational readiness wave 3 ([265ecd9](https://github.com/rknightion/synthkit/commit/265ecd93c6fd76d929b87a7afe344ae18af6bfcc))
* **docs:** take the fleet project icon for the site logo and favicon ([8388082](https://github.com/rknightion/synthkit/commit/8388082436b4586784be6275523f30ad256f7c67))
* **docs:** take the fleet-generated social card ([3872de1](https://github.com/rknightion/synthkit/commit/3872de1aef30082221e45f43eb71a4ca9da74634))
* expose delivery loss and queue pressure ([9488c2a](https://github.com/rknightion/synthkit/commit/9488c2af28f6a41fd2b238df8c6ddc4eb0e2806e))
* harden control plane exposure ([ef04f44](https://github.com/rknightion/synthkit/commit/ef04f44365ee4a6c961a7065d57b4d9b801578e5))
* improve operational onboarding wave 2 ([e98411b](https://github.com/rknightion/synthkit/commit/e98411b5cec4494be44497790117ca3ff91aa9c9))
* make deployment readiness explicit ([b6c4ea5](https://github.com/rknightion/synthkit/commit/b6c4ea576473230c1ec7fa91b647b96bd94cd76f))
* make optional Grafana lanes deployable ([189ecd5](https://github.com/rknightion/synthkit/commit/189ecd5c7e496412daa0e361180bf5325c0487b3))
* make upgrades reproducible and rollback testable ([729d4b1](https://github.com/rknightion/synthkit/commit/729d4b17a69736708882c7f6140e3ae610cabd74))
* mint release-please token from the OpenBao broker ([7316c0a](https://github.com/rknightion/synthkit/commit/7316c0a86c3d4c8c5bdfda9ecc0600030bbc0b97))
* mint the docs-sync token from the OpenBao broker ([68a661f](https://github.com/rknightion/synthkit/commit/68a661fe5092b98f55064d10213cbf35f0f9f170))
* require explicit blueprint selection ([9acd17f](https://github.com/rknightion/synthkit/commit/9acd17f85b7a98542a3d4d3a17a5343de3692cea))
* start operational readiness campaign ([9c93f5c](https://github.com/rknightion/synthkit/commit/9c93f5c94938cf6eb17ee275b54473f9c8dfe08d))


### Bug Fixes

* author is Rob Knight, not Rob Knighton ([9facdca](https://github.com/rknightion/synthkit/commit/9facdcac9f6c85dc74c230c7e34f251e68589c08))
* bind RC publisher dispatch to repository ([e0e87a9](https://github.com/rknightion/synthkit/commit/e0e87a97e700d5d68e918a3523ff2a410d3c3f2e))
* **deps:** update dependency @solidjs/router to v1 ([#66](https://github.com/rknightion/synthkit/issues/66)) ([1e776e7](https://github.com/rknightion/synthkit/commit/1e776e76c7603a2208e2022fb1e9ae562732fe94))
* **deps:** update module github.com/grafana/nanogit to v1.4.1 ([#60](https://github.com/rknightion/synthkit/issues/60)) ([465fa17](https://github.com/rknightion/synthkit/commit/465fa17ec2d068357206bc191f650eede9c2671d))
* **deps:** update module github.com/grafana/pyroscope-go to v1.4.1 ([#38](https://github.com/rknightion/synthkit/issues/38)) ([c0eb070](https://github.com/rknightion/synthkit/commit/c0eb070419567fe9bd3cad16ecda51c9ea4008d4))
* **deps:** update module github.com/grafana/pyroscope-go to v1.4.2 ([#82](https://github.com/rknightion/synthkit/issues/82)) ([7465974](https://github.com/rknightion/synthkit/commit/74659740f958b3b8bbf82159d836b9b8a7256010))
* **deps:** update module github.com/testcontainers/testcontainers-go to v0.44.0 ([#77](https://github.com/rknightion/synthkit/issues/77)) ([1a60e59](https://github.com/rknightion/synthkit/commit/1a60e59f923185d3e2b4f9136c7b2e3cc19ad69b))
* **deps:** update module go.opentelemetry.io/proto/otlp to v1.11.0 ([#59](https://github.com/rknightion/synthkit/issues/59)) ([ce1d4b3](https://github.com/rknightion/synthkit/commit/ce1d4b31187d5dd6d55018810aab9711b8a76ea6))
* **deps:** update module google.golang.org/protobuf to v1.36.12 ([#80](https://github.com/rknightion/synthkit/issues/80)) ([4be47d7](https://github.com/rknightion/synthkit/commit/4be47d788fcbcef6efd4bd59f8a8b4a0f7d306e5))
* make published Compose verification deterministic ([fedb517](https://github.com/rknightion/synthkit/commit/fedb517f451f829c1802fa4a7f8503c7a2e6b9dd))
* pass the JWT role explicitly for docs-sync ([6c20935](https://github.com/rknightion/synthkit/commit/6c209359f99fdf0b63f1c319f8464fe9c4bc2f8a))


### Refactor

* **agents:** replace the AGENTS.md symlink with the @AGENTS.md import ([a704865](https://github.com/rknightion/synthkit/commit/a704865eb1c134a01e69b141a1822c8793e7a7c9))


### Documentation

* add a "Why This Generator" positioning page ([830a9d3](https://github.com/rknightion/synthkit/commit/830a9d3247f20da0ea2fee169d12d6c6f08759bb))
* add an FAQ and a security page ([79ed716](https://github.com/rknightion/synthkit/commit/79ed716a5715c4de9c393a013a320e6695176037))
* adopt the m7kni.io inverted docs model ([3a8bb1e](https://github.com/rknightion/synthkit/commit/3a8bb1e272753e4d383eb509454b0860a793c14f))
* carry the AGPL note as license_note instead of overriding copyright ([50559d5](https://github.com/rknightion/synthkit/commit/50559d5c84c70f604a22b9b7d35b686d6bece1b4))
* close control-plane hardening task ([dae3302](https://github.com/rknightion/synthkit/commit/dae3302dd97792cb772c3a4e42f2bd79ff9ee6be))
* close fresh-start deployment task ([5e2be0c](https://github.com/rknightion/synthkit/commit/5e2be0cbb38692f814d936df3b7ffd1ae9591baa))
* delete issue [#26](https://github.com/rknightion/synthkit/issues/26) from GitHub, make the closed-work doc the record ([aada9f2](https://github.com/rknightion/synthkit/commit/aada9f2499680b98ef3b0814060ac81ff4d697f7))
* finalize operational readiness wave 2 ([1569d34](https://github.com/rknightion/synthkit/commit/1569d344ccab04addf301c43753c945b3a7f1892))
* finalize operational readiness wave 3 ([905cf21](https://github.com/rknightion/synthkit/commit/905cf21dcccf751d7eb9483fbe5253cafe28507b))
* finalize operational readiness wave A ([225cd62](https://github.com/rknightion/synthkit/commit/225cd624d98a2041ce117e850c796b5509b578a9))
* finalize optional Grafana lanes ([a707490](https://github.com/rknightion/synthkit/commit/a70749029c5805b4b9e1ec2318f57f23c015c1cb))
* finalize strict HTTPS e2e repair ([8ea671f](https://github.com/rknightion/synthkit/commit/8ea671f859c4a967ee0f5bef85171b04e4c6f426))
* fix broken internal links ([b690f69](https://github.com/rknightion/synthkit/commit/b690f6974cc98b045dd5c528c625feb4bcb42668))
* match the stated Go version to go.mod ([16f504a](https://github.com/rknightion/synthkit/commit/16f504a47f9c2555e2b5999b2916b03aa6032449))
* put a copy-paste quickstart on the landing page ([a75ec94](https://github.com/rknightion/synthkit/commit/a75ec94417236c1396e79a1f6a4e691c8c42fe6a))
* re-import fan-out protocol (context-cost rules) ([a529c8f](https://github.com/rknightion/synthkit/commit/a529c8f080e1e4719248ee1d118601f2dcbbb42c))
* re-render the fan-out protocol from agent-docs ([3108bb9](https://github.com/rknightion/synthkit/commit/3108bb944a8fab1fcfb3bbd3dd244cd32678ea56))
* re-render the fan-out protocol from agent-docs 711db6c ([99bdf51](https://github.com/rknightion/synthkit/commit/99bdf515fa217cb05d40393e9a8b010dfddb8721))
* re-render the fan-out protocol from agent-docs b0d76d8 ([65df024](https://github.com/rknightion/synthkit/commit/65df0245a9048cec0a9cc94376c3e95483934c91))
* **readme:** lead with what the project is ([1ccf84b](https://github.com/rknightion/synthkit/commit/1ccf84b1f1bce0b8e0ddc28ca44f1a2aec09ed6a))
* **readme:** link the documentation site ([a632a33](https://github.com/rknightion/synthkit/commit/a632a33662d8afdc0252d165d2ef11e19c8241bd))
* record live control-plane verification ([93f957f](https://github.com/rknightion/synthkit/commit/93f957f05a87dca776d2134d80fc3a628363771a))
* record live self-observability verification ([9b35176](https://github.com/rknightion/synthkit/commit/9b3517667d6243e6fad12890859f6de24cd7465b))
* remove deployment identifiers from public records ([d4d48c5](https://github.com/rknightion/synthkit/commit/d4d48c5c684b5533ef7b470c181e243ee89f4b1d))
* remove private host name from backlog ([5667903](https://github.com/rknightion/synthkit/commit/56679036c56815d02b4e0d9d3d0adad2fb131219))
* something ([f13da00](https://github.com/rknightion/synthkit/commit/f13da008eaf4db62ec0c377b97a22f8585598e86))
* **tracker:** align canonical fan-out protocol ([4ac48a2](https://github.com/rknightion/synthkit/commit/4ac48a26ae6bf697f2518439d7b4a2e1068de873))
* **tracker:** correct the canonical owner in the rendered header ([d7442cd](https://github.com/rknightion/synthkit/commit/d7442cd2bb58327ea411ffce6b898f438e0d31eb))
* **tracker:** normalise the closed-issues doc title ([cbd376a](https://github.com/rknightion/synthkit/commit/cbd376a4fc1e36326e68625ecd1e53c045f9cf00))
* **tracker:** re-import the fan-out protocol from canonical ([64d0de2](https://github.com/rknightion/synthkit/commit/64d0de29e597620aa240f4c21ec273ef675fb473))
* **tracker:** render agent documents from the canonical source ([f0b48d7](https://github.com/rknightion/synthkit/commit/f0b48d7964f12e7731703419a6983ee473f057ef))


### Build & CI

* add auto-rc, arm-automerge and ghcr-cleanup ([d698adb](https://github.com/rknightion/synthkit/commit/d698adbd6dd82fa4d91c5022931224222e4e558d))
* docs-sync and grafana-sync targets moved to the m7kni org ([6e37c0f](https://github.com/rknightion/synthkit/commit/6e37c0f346f59191dddffc8f2aa52a745e3f61db))
* repin the release-automation reusables to v1.8.0 ([31539d7](https://github.com/rknightion/synthkit/commit/31539d7c2e5f88cd426eb1882e49cd58326ac495))

## [1.2.0](https://github.com/rknightion/synthkit/compare/v1.1.0...v1.2.0) (2026-07-03)


### Features

* **docs:** align docs site with m7kni.io brand + server-side SEO/LLM metadata ([67e2144](https://github.com/rknightion/synthkit/commit/67e21448e5c163f224a66d081742f6dd119bfca0)), closes [#26](https://github.com/rknightion/synthkit/issues/26)


### Bug Fixes

* **deps:** update module github.com/grafana/pyroscope-go to v1.4.0 ([#29](https://github.com/rknightion/synthkit/issues/29)) ([cfd6851](https://github.com/rknightion/synthkit/commit/cfd6851996cd8638b0fc4d2c90328c0c90aeb052))


### Documentation

* **geo:** content-shape pass for LLM/search retrievability ([da03919](https://github.com/rknightion/synthkit/commit/da03919070de710eabf96f5fbb54297edd0098a8))


### Build & CI

* add OpenSSF Scorecard via shared reusable workflow ([0ca0911](https://github.com/rknightion/synthkit/commit/0ca0911dcbfdb81e22737f8f7d038e3a17ba5272))
* drop CodeQL pull_request trigger to trim Actions fan-out ([f2ab6dc](https://github.com/rknightion/synthkit/commit/f2ab6dcc00349074efeb5582f50462ff2dd1547d))
* **renovate:** remove local pr limits + minimumReleaseAge pin ([9d740af](https://github.com/rknightion/synthkit/commit/9d740afe984f174f5df63ea4cc33edd6450292c0))

## [1.1.0](https://github.com/rknightion/synthkit/compare/v1.0.0...v1.1.0) (2026-06-30)


### Features

* initial public release ([58765c4](https://github.com/rknightion/synthkit/commit/58765c41ecdd840c690c40027ba3ed635e176619))
* sigil (Grafana AI Observability) signal + grafana-ai-o11y blueprint ([#21](https://github.com/rknightion/synthkit/issues/21)) ([379bce7](https://github.com/rknightion/synthkit/commit/379bce74b17fe75ea86433df9f719e860f4e0ffa))


### Bug Fixes

* **deps:** update module github.com/golang/snappy to v1 ([#17](https://github.com/rknightion/synthkit/issues/17)) ([1fa866f](https://github.com/rknightion/synthkit/commit/1fa866f61c39e59b868f3d03986cfa44d6a9e538))
* **deps:** update module github.com/grafana/nanogit to v1.4.0 ([#12](https://github.com/rknightion/synthkit/issues/12)) ([180ec23](https://github.com/rknightion/synthkit/commit/180ec23e8df473b71b324bdd00e26c9bc7439f45))
* **deps:** update module github.com/testcontainers/testcontainers-go to v0.43.0 ([#13](https://github.com/rknightion/synthkit/issues/13)) ([069c74d](https://github.com/rknightion/synthkit/commit/069c74d1ca2d2ebbc0df110d81027697cb6f17c3))
* **security:** rename theme localStorage key constant to clear Snyk secret heuristic ([389f658](https://github.com/rknightion/synthkit/commit/389f65896ba988c85f1a297707dac09da2d42e40))
* **security:** render sparklines as JSX &lt;svg&gt; instead of innerHTML ([7932d3f](https://github.com/rknightion/synthkit/commit/7932d3f652d437b7a35ab46331b7e76aa8d0dcd4))


### Refactor

* complete golden-thread scrub (routes/identifiers) + rename "Golden Path" → "Connected Gateway" ([f5e7d88](https://github.com/rknightion/synthkit/commit/f5e7d883896f0ebbd3de7111d4d3ca342a060b4a))


### Documentation

* add zensical documentation site + m7kni-net-site sync trigger ([c05c5b3](https://github.com/rknightion/synthkit/commit/c05c5b3d99c2a74aeaf606d7a01174a157877e50))
* fix runbook accuracy against source ([ea0726f](https://github.com/rknightion/synthkit/commit/ea0726fa5cd8d5000c6bab87b5636a002c41d10d))
* scrub "golden thread" jargon from the signal catalogue and docs ([3493bd7](https://github.com/rknightion/synthkit/commit/3493bd73ff3ca4e20a1700bcbd43986dab2081c5))


### Build & CI

* add aggregator job for branch rules ([08c24ee](https://github.com/rknightion/synthkit/commit/08c24ee012e0f264d8c5fdb404de2083c69128fe))
* add Snyk -&gt; Snyk Cloud monitor (SCA/SAST/IaC/container) ([1c8f09f](https://github.com/rknightion/synthkit/commit/1c8f09f12a54da529da2cd865ed25af9cf47d8c0))
* build + publish edge :main image on push to main ([712a0cc](https://github.com/rknightion/synthkit/commit/712a0ccbefdaab54848e07458407ef8f31a15e55))
* **codacy:** add Go coverage upload + tune repo-local exclusions ([fa67535](https://github.com/rknightion/synthkit/commit/fa675353a5de6f260d476bf1bfc0169fad75cd8e))
* drop internal/integration from the -race leg (OOM) ([6b9a4b0](https://github.com/rknightion/synthkit/commit/6b9a4b04f77b85300448b51e243c0f523d80f4c1))
* open the release-please PR under a PAT so CI runs without manual approval ([342f2e9](https://github.com/rknightion/synthkit/commit/342f2e9089b06849baa64645c06f02d9346fded9))
* pin shared rknightion reusables to v1.0.0 ([f370981](https://github.com/rknightion/synthkit/commit/f370981169f1bd7de477b3b9ce986fd69599c2c8))
* publish via shared container-publish reusable (guinea-pig) ([6510d91](https://github.com/rknightion/synthkit/commit/6510d91f631f31d97b15e8c48f7480d82221df74))

## [1.0.0] - 2026-06-29

### Features

- Initial public release of synthkit — composable synthetic telemetry generator for Grafana Cloud.
  Declare infrastructure and applications in YAML blueprints; synthkit emits structurally-correct
  synthetic metrics (Prometheus Remote-Write v2), traces (OTLP), and logs (Loki) for whatever each
  declared construct supports.

### Constructs included

- AWS: EC2, RDS, ElastiCache, CloudWatch infrastructure (ALB/EBS/NAT/EKS/S3/Firehose)
- Azure: CSP Azure (VMs, App Service, SQL, Storage, Cosmos DB, Functions)
- GCP: CSP GCP (Compute, Cloud SQL, Cloud Storage, Pub/Sub, Cloud Run)
- Kubernetes: cluster metrics, k8s-monitoring, addon correlation (cert-manager, Karpenter, ArgoCD, Envoy Gateway, CoreDNS, AWS LBC, external-dns, cluster-autoscaler)
- AI/LLM: gen_ai request flow, Portkey gateway, LangSmith evaluation, Bedrock, AgentCore, Snowflake Cortex, LangGraph
- Grafana products: dbo11y, Fleet Management, Synthetic Monitoring, Faro RUM, Beyla eBPF
- Network: network_topology (SNMP topology)
- Application: web_service, web_vitals

### Build & CI

- AGPL-3.0-only license + full OSS governance apparatus (CONTRIBUTING.md, CODE_OF_CONDUCT.md, SECURITY.md)
- SPDX headers enforced on every `.go` file via `scripts/spdx-check.sh`
- Forbidden-words hygiene guard (`scripts/forbidden-words.sh`) — credential shapes always-on, deployment identifiers via CI secret
- release-please changelog automation + Renovate dependency management
- GitHub Actions: release-please, publish (GHCR multi-arch image), CodeQL, zizmor, actionlint, dependency review
