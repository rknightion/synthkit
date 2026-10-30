# Changelog

All notable changes to synthkit. Generated from Conventional Commits.

## [1.6.0](https://github.com/rknightion/synthkit/compare/v1.5.1...v1.6.0) (2026-10-30)


### Features

* add boot-only startup check ([e0cb434](https://github.com/rknightion/synthkit/commit/e0cb434cb46858992e5b0c2418222175b5221a3c))
* add bounded app agent pipeline component ([2bb1d28](https://github.com/rknightion/synthkit/commit/2bb1d280c8403c888cf4fe05acab99032e2b77c3))
* add bounded CloudWatch Logs intake consumer ([75c6e8b](https://github.com/rknightion/synthkit/commit/75c6e8bb8dcc0721b1b3479f86f2e6359617ef77))
* add bounded evaluator and hook management transport ([1e5ff86](https://github.com/rknightion/synthkit/commit/1e5ff86fce2ff836f7b19fa6c50e78cadfa72448))
* add bounded provisioned Redshift consumer ([5ef747a](https://github.com/rknightion/synthkit/commit/5ef747a7d46aed87a03ef777824f641f3fa17dcd))
* add captured DynamoDB CloudWatch consumer ([1160991](https://github.com/rknightion/synthkit/commit/1160991c65c79c7636ec25084cf08b35734960c5))
* add captured Lambda CloudWatch consumer ([55a12f3](https://github.com/rknightion/synthkit/commit/55a12f3312c38cbf7e3e64719418ca830356e0ef))
* add captured SQS CloudWatch consumer ([732d670](https://github.com/rknightion/synthkit/commit/732d6707e2194006c4e1298590693cfea91a0f2a))
* add delivery acknowledgements and checkpoint storage foundations ([86634ea](https://github.com/rknightion/synthkit/commit/86634ea40952074beedeb4751609f63b5b9cf8ac))
* add explicit Lambda Logs delivery period observations ([df38645](https://github.com/rknightion/synthkit/commit/df3864542b26b2528c74ae273435f761594ac9a8))
* add fictional regulated-document corpus and gzip privacy checks ([e009f19](https://github.com/rknightion/synthkit/commit/e009f1955fc883aa8bb55cd8454272c37d18abfe))
* add local provisioner custody foundations ([feaaf41](https://github.com/rknightion/synthkit/commit/feaaf4189b1f6b5a97aa1471f31093adaa9156ec))
* add remaining agent management resource transport ([c52ed18](https://github.com/rknightion/synthkit/commit/c52ed18ba451793190eecad3d6c48f34ce16691a))
* add sourced Solace event broker construct ([0b4ec1a](https://github.com/rknightion/synthkit/commit/0b4ec1ac7336a0939ab34fa796a0a5d391f3ffc2))
* admit sourced blueprint-owned app metrics ([f9332c0](https://github.com/rknightion/synthkit/commit/f9332c028900c3b6b5b37e738d94430c31111711))
* **agentcore:** bind a runtime to a causal concurrency resource ([a968eb8](https://github.com/rknightion/synthkit/commit/a968eb835eee74f4b7c93f5f6d889d2d58a1a950))
* **agento11y:** add the source-built manual Agent Observability provisioning command ([ff59d73](https://github.com/rknightion/synthkit/commit/ff59d73c5d32780673fee0cd2be80c542c71d05b))
* **agento11yprovision:** add private typed lifecycle engine ([e8e0921](https://github.com/rknightion/synthkit/commit/e8e0921951b47d58329353c0166ff3378c078c3c))
* allow boot-only chart startup command ([4beddb1](https://github.com/rknightion/synthkit/commit/4beddb10940f74411d276405d8a474b891aa3ded))
* **app:** add collector self-telemetry value sources ([6ff3694](https://github.com/rknightion/synthkit/commit/6ff3694de095a74fb9b028074a0d25e429dfca88))
* **app:** attribute a downstream service's error rate to its own hop ([5e42169](https://github.com/rknightion/synthkit/commit/5e42169d1f70668f5e68ccfe11ffa7a3df8febab))
* **app:** derive request outcomes from causal resource state ([a47bdb0](https://github.com/rknightion/synthkit/commit/a47bdb06e39d89329e7dbcb7814989816c63ea44))
* **app:** draw per-request values shared by a log bundle ([66e879f](https://github.com/rknightion/synthkit/commit/66e879f3750fdcc78a3fcb72ca808063d37722d5))
* **app:** drive histogram observation counts from matching counter series ([c84980a](https://github.com/rknightion/synthkit/commit/c84980affbf4ed4266a9c4c1a9e745ba95f4f0b4))
* **app:** emit metadata-only thinking parts as empty thinking payloads ([5f7ad73](https://github.com/rknightion/synthkit/commit/5f7ad73b845f4200b9835237fccf1bdda2943e29))
* **app:** gate bundle lines on per-request draws and keep logical identity across client retries ([89aa693](https://github.com/rknightion/synthkit/commit/89aa6939954a36b9f89be71a2fecce5571d0806f))
* **app:** give a custom metric or enum value its own business clock ([438a0ab](https://github.com/rknightion/synthkit/commit/438a0ab9aedcd5eff2f540b81b8be7a943066312))
* **app:** give bundle lines their own event time from per-line dwell ([8ba61ba](https://github.com/rknightion/synthkit/commit/8ba61ba893046ad3f1377b9c7aa00d333e343d35))
* **app:** hold a now gauge while a declared stall incident is active ([0549cba](https://github.com/rknightion/synthkit/commit/0549cbaa6c3093d4114545616d439dc7fa073c46))
* **app:** join synchronous invocation traces inside their caller ([e3700dd](https://github.com/rknightion/synthkit/commit/e3700dd68ed944aadba597a09a48ea255e318815))
* **app:** let a count_of histogram follow a counter over a label subset ([b4605ac](https://github.com/rknightion/synthkit/commit/b4605aca7ccde4cf5053b7ca8c2e2636b7ada9aa))
* **app:** mint queue-driven requests from the served whole-event count ([bfc34f9](https://github.com/rknightion/synthkit/commit/bfc34f99c2c1da1ce867f8e95055aa8ffe832d59))
* **app:** model whole-item stocks that accumulate under a stall and drain on recovery ([f1fe8c6](https://github.com/rknightion/synthkit/commit/f1fe8c6cdeb35675bda92f5944fe87adb35e536a))
* **app:** opt-in absolute incident level for shape values ([15027f9](https://github.com/rknightion/synthkit/commit/15027f9af017e14f245ab077a7544dc8fb259dc2))
* **app:** per-value scales, stock gauges, load profiles, lognormal and entry error rate ([f91b425](https://github.com/rknightion/synthkit/commit/f91b425cc2f720c0c64ff7f69e77e49ea110e928))
* **app:** project causal outcomes into spans, logs and metrics ([856d4ce](https://github.com/rknightion/synthkit/commit/856d4ce9d30ecde44b29ab51f3cee2b9a3d241e7))
* **app:** propagate trace context from an app hop into the worker it triggers ([be730c5](https://github.com/rknightion/synthkit/commit/be730c5667da93d0c5d0cc40ad3c4fcc0608aece))
* **app:** seed item stocks from the arrival curve over the residence window ([8c8664b](https://github.com/rknightion/synthkit/commit/8c8664b0cd33175c56761669fe7c3ec2b195ba14))
* **app:** step a last-success timestamp on a declared schedule ([4e2355e](https://github.com/rknightion/synthkit/commit/4e2355eb3312222471acc77b2e29422d4529ffcb))
* **blueprint:** admit coherent causal database observability ([b34dfa4](https://github.com/rknightion/synthkit/commit/b34dfa458e7a06e694334182c5ce9267c9911e06))
* **blueprintschema:** render duration fields as durations ([e70f944](https://github.com/rknightion/synthkit/commit/e70f944edb97949163cb329757707de3ca47e0bb))
* **blueprint:** warn on rate-only scenarios and reject wrong-kind resource faults ([b4d5afa](https://github.com/rknightion/synthkit/commit/b4d5afaa8f753f35fbf822bc9c055320985c18d9))
* **causal:** add the shared causal resource model, blueprint resources and runner advance hook ([ce76e57](https://github.com/rknightion/synthkit/commit/ce76e572fac28373c351b37a83e0428f27927a11))
* **causal:** count caller resends of subtree failures as demand and hold failed work only until it fails ([fb2ba6f](https://github.com/rknightion/synthkit/commit/fb2ba6f9a95b0f7b94369c508e0f2cf87f685563))
* **causal:** derive a bound resource's demand from upstream admitted calls ([c32f1b9](https://github.com/rknightion/synthkit/commit/c32f1b98dc73095d3f1bccbcc0d929735ff1f707))
* **chart:** optional synthkit-chat sidecar beside the emitter ([6b6c1d5](https://github.com/rknightion/synthkit/commit/6b6c1d5f12d48d599345460fd20690ee02a197e6))
* **chatsim:** keep the control panel the newest message in the DM ([8df892f](https://github.com/rknightion/synthkit/commit/8df892fa62fff8cf1167b8885b7b72d3b3008fa5))
* **chatsim:** open floor, request summons, control DM with fire/heal/direct, start and fire phrases ([ebd65b3](https://github.com/rknightion/synthkit/commit/ebd65b33f17e957571b4e560317330ae35626696))
* **chatsim:** persona pictures, and IRM reset resolves instead of deleting ([1aa8018](https://github.com/rknightion/synthkit/commit/1aa8018439f2dbedb09767b226e7184486657adf))
* **chatsim:** pinned control panel in the control DM ([9808a05](https://github.com/rknightion/synthkit/commit/9808a050cc8e0d01bc45c4ba486c44eb1cd26e16))
* **chatsim:** reset can also clear Grafana IRM ([9bb1652](https://github.com/rknightion/synthkit/commit/9bb1652511482e7466076dae770af6cba7d7363b))
* **chatsim:** reset the workspace to a seed history from the control panel ([1a72c22](https://github.com/rknightion/synthkit/commit/1a72c22e9edfd45097ddee77c28ad5261db6a556))
* **chatsim:** scenario loader, deterministic engine, director and golden test ([e92203b](https://github.com/rknightion/synthkit/commit/e92203b3d34d813b789167c505fa27cf9265c761))
* **chatsim:** seed lines can replay another app's post with its name, picture and blocks ([4ce2d1f](https://github.com/rknightion/synthkit/commit/4ce2d1f2ff292318ad50e45eb2615fab563c0ca8))
* **chatsim:** synthkit-chat binary with Slack, console, OpenRouter and SynthKit trigger ([c101a1b](https://github.com/rknightion/synthkit/commit/c101a1b7b6d2932f8fdfb6fa8561d7cd899b8d08))
* **cli:** bound validation and preflight by an absolute deadline ([eb096f7](https://github.com/rknightion/synthkit/commit/eb096f7dd856bb067dde68d6267c6cdf3bdfb68d))
* **control:** add opt-in per-blueprint flags to the readiness probe ([a4ce297](https://github.com/rknightion/synthkit/commit/a4ce2978e55d71ce210db1ae56728d705b5ac7e8))
* **control:** admit Synthetic Monitoring in live blueprint apply ([1d627bf](https://github.com/rknightion/synthkit/commit/1d627bf1d09c32279c9e4f952863baf0f0baeb2c))
* **control:** apply and remove one namespaced blueprint live ([29583af](https://github.com/rknightion/synthkit/commit/29583afda20c9e0e92cdb4a322af97daa268fc8e))
* **control:** disable construct kinds from the first tick at startup ([b9a47b2](https://github.com/rknightion/synthkit/commit/b9a47b282465cab92db3615d426240d2f085f95d))
* **control:** expose each causal resource's bound consumers in the control schema ([188ebfd](https://github.com/rknightion/synthkit/commit/188ebfd3b7c828558b71c8933c91110f80a3d250))
* **cw:** sourced Lambda zero-valued samples and DynamoDB throttle metrics ([0826600](https://github.com/rknightion/synthkit/commit/08266004d39d92172ef54911d10612e0ac367c26))
* **dash:** add deterministic dashboard UID namespaces ([9d5a31f](https://github.com/rknightion/synthkit/commit/9d5a31fba8fc76b87c12d876714803c5d1266579))
* **databases:** bind rds and docdb instances to causal resources ([16ce2b1](https://github.com/rknightion/synthkit/commit/16ce2b175dca046c4fc1ec09e5569b3b3d57a053))
* **datadog:** emit standalone-host CPU, load and memory families from declared capacity ([5a6615b](https://github.com/rknightion/synthkit/commit/5a6615bd891daa0eaba543255fe89a43b13a24ee))
* **datadog:** emit standalone-host file-handle families from file-nr ([d949c75](https://github.com/rknightion/synthkit/commit/d949c7537ce61529b5b9155fa31917ec96a03801))
* **dbo11ymysql:** bind observability to causal resource state ([0dbe95f](https://github.com/rknightion/synthkit/commit/0dbe95f6fe945f0b67ff5687072807dad14b7b74))
* **dbo11ypg:** add private causal observability adapter ([507db1c](https://github.com/rknightion/synthkit/commit/507db1c2a5c071298e48406538f7653258ab637a))
* discover complete pure agent declarations ([a0ad212](https://github.com/rknightion/synthkit/commit/a0ad2120fd63530d88ca46c8ff15eded702ca797))
* **dynamodb:** add a table throttle incident axis and UpdateItem operations ([e6e98cd](https://github.com/rknightion/synthkit/commit/e6e98cd7f719ab8623f1bb86415db809a680f02c))
* **dynamodb:** bind table operations to causal quota resources per request or capacity unit ([fa5e649](https://github.com/rknightion/synthkit/commit/fa5e6490cace2c08b61dd62b5947c82a87d2a3c5))
* **dynamodb:** opt-in business-curve load and latency spread ([903bb60](https://github.com/rknightion/synthkit/commit/903bb6028a5d59305d6e87184a4aa712a924adeb))
* emit sourced H100 HGX DCGM profiles and faults ([5e2bb9a](https://github.com/rknightion/synthkit/commit/5e2bb9a6a9c9b78168d55601b34efe83089965dd))
* emit sourced MWAA terminal outcome observations ([92a40ad](https://github.com/rknightion/synthkit/commit/92a40addab59e8d0dbca4b7167b18fe6def9d28c))
* **failuremode:** assert causal resource modes in the live scenario check ([d03adc4](https://github.com/rknightion/synthkit/commit/d03adc47a0bba771085de7bfeb48870c05e924d8))
* **fleet:** tear down every collector the ownership ledger records ([8d0048a](https://github.com/rknightion/synthkit/commit/8d0048a1bde45eac633af485adf5a71fbd97b0d1))
* freeze neutral agent declarations and conversation rating ABI ([8305a90](https://github.com/rknightion/synthkit/commit/8305a909400b7243a587c03f0591f60a26b16c5d))
* **ha:** report live-restore diagnostics and optional lanes on the leader ([2ff0839](https://github.com/rknightion/synthkit/commit/2ff08397204ec1249b99f6e21697390483681a43))
* **incidents:** function and queue axes so one incident drives Lambda and Solace ([71753e1](https://github.com/rknightion/synthkit/commit/71753e17a3c5a49be26f2564e66b07d5d85e75b2))
* integrate bounded Redshift and Logs CloudWatch consumers ([ce0520a](https://github.com/rknightion/synthkit/commit/ce0520adc65a4352aa45ce13c08ca5ec62aacce6))
* **lambda:** bind a function to a causal concurrency resource ([02415b2](https://github.com/rknightion/synthkit/commit/02415b2748dfb51530e5c654368c2daef513c7c1))
* **lambda:** keep a background error share on bound functions ([7f7606d](https://github.com/rknightion/synthkit/commit/7f7606d73ab09d54e1c4b4f369366c1a731f99a1))
* **lambda:** let a scheduled function's duration follow the curve at constant invocations ([7f850b8](https://github.com/rknightion/synthkit/commit/7f850b882064c042d5985f126749abe1444c6e2a))
* **lambda:** let scheduled function duration follow the load curve ([c7717cf](https://github.com/rknightion/synthkit/commit/c7717cf182248e96ae8dc40d38b89cb1fc776dfe))
* **lambda:** opt-in business-curve load and duration spread ([3b3d753](https://github.com/rknightion/synthkit/commit/3b3d753a6ae2778344eb59abe5ee59458c2553d6))
* model per-agent Bedrock inference profiles ([fbf50d8](https://github.com/rknightion/synthkit/commit/fbf50d825f09fc265f05fce3538d2e9b0a742a7b))
* **otlp:** encode span events ([ebca954](https://github.com/rknightion/synthkit/commit/ebca954187e12b93bd2e992ad1cadfb35d674303))
* refresh current Agent Observability contracts ([47e386f](https://github.com/rknightion/synthkit/commit/47e386fffaaeeac9ef07429c964efafa5b461af9))
* **scheduler:** EventBridge Scheduler construct with captured invocation attempts ([9e3dad5](https://github.com/rknightion/synthkit/commit/9e3dad539837c4dd50a88f71a0152ffa06c67a55))
* **selfobs:** expose the leadership session on operational telemetry ([c0c9712](https://github.com/rknightion/synthkit/commit/c0c97125cc9f51a5a5055536b12ce16dddaeeeee))
* **selfobs:** publish a bounded manifest of intended delivery lanes ([c5ddebc](https://github.com/rknightion/synthkit/commit/c5ddebc8ac76b710cd023948b651193572b2b380))
* **shape:** add batch-shaped arrivals on a series clock ([3679b02](https://github.com/rknightion/synthkit/commit/3679b0200d6c0051ac4f6b028898dddae549606e))
* share declared pipeline timelines across Glue and MWAA ([93a7f34](https://github.com/rknightion/synthkit/commit/93a7f340defe1256fe57481fcf72824ea5f48723))
* **sigil:** add hooks:evaluate and conversation rating transports ([19a18a3](https://github.com/rknightion/synthkit/commit/19a18a3a2ed6788bf370fcab1e70055dbe8f68a4))
* **signals:** machine-readable opt-in catalogue blocks checked against the engine ([85a515a](https://github.com/rknightion/synthkit/commit/85a515ac07481edec72e175e01ee010aea98c042))
* **sm-provision:** add ownership-bound preview and explicit cleanup ([bebf4c3](https://github.com/rknightion/synthkit/commit/bebf4c39acb9ca98b1fc67e20b5186a4d9b9fe9c))
* **sm-provision:** clean up after SM is disabled and package cleanup in the chart ([0abb9d7](https://github.com/rknightion/synthkit/commit/0abb9d7547f47fcfeaa878b09d5b3b50444d998c))
* **sm:** admit live Synthetic Monitoring per blueprint ([e8c4a8e](https://github.com/rknightion/synthkit/commit/e8c4a8ee9ddc05859dcfbac4b0d08b3e0afd4977))
* **solace:** bind a queue to a causal queue resource and drain on a schedule ([af411b6](https://github.com/rknightion/synthkit/commit/af411b69f312e4e1a0e5a2b70a321ff3cd269c75))
* **solace:** give each queue its own rate scale and business clock ([b08c061](https://github.com/rknightion/synthkit/commit/b08c061f30118a0d78e755ddf4766ae99c95c474))
* **sqs:** bind a queue to a causal queue resource ([ab239ea](https://github.com/rknightion/synthkit/commit/ab239eafbb5b5254646bc827af072e69c2c588b4))
* **sqs:** deliver arrivals in periodic bursts ([f323dd7](https://github.com/rknightion/synthkit/commit/f323dd792b993f22919b7d5c200b6f407f996912))
* **sqs:** opt-in business-curve load for declared queues ([f172f84](https://github.com/rknightion/synthkit/commit/f172f8407134f93f9ad3716cf670deac46121f1b))
* support explicit incident endpoints for custom metric shapes ([691dc9a](https://github.com/rknightion/synthkit/commit/691dc9aa7062085d1a8c2228872c515462066b88))
* **telemetryspec:** add a now gauge value kind for heartbeat timestamps ([1cccc81](https://github.com/rknightion/synthkit/commit/1cccc81d7465e7ab82ff40a3307bc3727ad31313))
* **telemetryspec:** add resource and outcomes metric value kinds ([6a252e5](https://github.com/rknightion/synthkit/commit/6a252e5ae6e63d3a522ad1ac4617cf299516d86d))
* wire bounded captured CloudWatch consumers ([72108ab](https://github.com/rknightion/synthkit/commit/72108ab492778264e2a37dca39c712f3429832ab))


### Bug Fixes

* **agentcore:** include failed invocation latency in bound metrics ([db68c11](https://github.com/rknightion/synthkit/commit/db68c110095dc3edbb25df8c3f71ebeb264f07d2))
* **agento11yprovision:** avoid redundant canonical custody scan ([681d26e](https://github.com/rknightion/synthkit/commit/681d26eadbf4d372395d27b3813b742424460618))
* **agento11yprovision:** keep the interruption cause on unknown custody outcomes; add flake diagnostics ([83d69fc](https://github.com/rknightion/synthkit/commit/83d69fca846c5b435268b19d25ea25f30f42a0bb))
* **agento11yprovision:** retain exact-read error causes ([158dc0f](https://github.com/rknightion/synthkit/commit/158dc0f024de650e18eedca051cc52ae9bdc348a))
* **aiagent:** pause ai_agent at an explicit volume_multiplier of 0 ([74d8af9](https://github.com/rknightion/synthkit/commit/74d8af925c62f2da340d81e3c800d53e89dd2236))
* **app:** accept error_rate on a sole service without an entry flag ([9929c84](https://github.com/rknightion/synthkit/commit/9929c84f1dd21292c4799520dc6a45f1bcdb1fc6))
* **app:** align gateway export log status with causal request failures ([81fece7](https://github.com/rknightion/synthkit/commit/81fece7878914eed27ef7c70d9783f3280647de8))
* **app:** apply gRPC status rules and keep faulted log draws off the engine ([3cf8d32](https://github.com/rknightion/synthkit/commit/3cf8d322727e7d74f9013ae8ab1367d2e298afb3))
* **app:** bound client retry backoff to the call policy cap ([a53ef19](https://github.com/rknightion/synthkit/commit/a53ef19934f4e8ccf97208555086de6143a6c6d8))
* **app:** count a held pool's dependency slowdown once ([b3249d0](https://github.com/rknightion/synthkit/commit/b3249d0bcca11e49ae5e2d21325abf3ac38adae4))
* **app:** expire synchronous calls by observed publication identity ([480468e](https://github.com/rknightion/synthkit/commit/480468ec360c36352c07d77fcab32e9b084822ea))
* **app:** give background and causal failures their error fields and omit empty log refs ([d4a51b3](https://github.com/rknightion/synthkit/commit/d4a51b34ebb839895c435b46dd14df9c350db7c1))
* **app:** keep a joined worker's trigger when one tick joins more than the trigger book holds ([da301fc](https://github.com/rknightion/synthkit/commit/da301fc4aea99fc1baf2681762d38c7dd9b61425))
* **app:** keep batch releases, stock memory and count_of bounds within their documented limits ([1600401](https://github.com/rknightion/synthkit/commit/1600401e08129864d20f922ea8416714a8c82e57))
* **app:** keep causal span status and draws aligned with semconv and legacy ([6865ed4](https://github.com/rknightion/synthkit/commit/6865ed44b0e4370e820ac325fd85d4383f00ea6d))
* **app:** keep handled rejection exemptions local to their origin ([f3957a8](https://github.com/rknightion/synthkit/commit/f3957a8a5e4992d3a469e5ebf0c32421ea5d3128))
* **app:** keep synchronous child spans inside their parent span ([b7ca5ba](https://github.com/rknightion/synthkit/commit/b7ca5bac41cc26225879684bae58fb6fcf8b642c))
* **app:** keep unaffected span-metric rows on the legacy path in faulted windows ([27c05e8](https://github.com/rknightion/synthkit/commit/27c05e8743ed760be14d80fb52376599f581c591))
* **app:** lay healthy causal requests on the engine timeline so untouched spans keep healthy duration ([3c0f4ea](https://github.com/rknightion/synthkit/commit/3c0f4ea66e0a5a50decab0be7277655d8db712ac))
* **app:** name a worker entry's consumer root span for its queue operation ([6f759e6](https://github.com/rknightion/synthkit/commit/6f759e657ec5dd366579538ce623997a701f9263))
* **app:** put baseline failures on the causal timeline ([29aa1ec](https://github.com/rknightion/synthkit/commit/29aa1ec5f134f36f3365d7ac510a8bc8802b924b))
* **app:** reject unsupported database trace identities ([4423808](https://github.com/rknightion/synthkit/commit/44238083309dc08bf91c47bca1b72f752ac8e53d))
* **app:** retain baseline failure logs on unaffected paths ([221f373](https://github.com/rknightion/synthkit/commit/221f373cda745a87d9e60f5b0ef2bcfd08fc0721))
* **app:** round causal hop endpoints consistently ([976f53f](https://github.com/rknightion/synthkit/commit/976f53fe22f9991fead7ec46fa0d23cf21d954ec))
* **app:** scale client span-metric rows and graph edges only by their own caller's faulted hops ([31cd041](https://github.com/rknightion/synthkit/commit/31cd04106ee8b32cb8989b7df48d08509a876406))
* **app:** use declared healthy references before learning ([255ad1c](https://github.com/rknightion/synthkit/commit/255ad1ce3c1f0e1a77c7bfda5a6078249f116f44))
* **blueprint:** reject unsafe legacy throttle scope on bound runtimes ([9d81857](https://github.com/rknightion/synthkit/commit/9d8185727522f30e8eb7b731ff7f3f8c293904db))
* **causal:** align rejection reads and elapsed timeout resend demand ([bc05827](https://github.com/rknightion/synthkit/commit/bc0582718f4b906a2f31bcb93bc6e53271793344))
* **causal:** bound live faults in warm start and tighten resource validation ([40a4d8d](https://github.com/rknightion/synthkit/commit/40a4d8da80d3853c13cd2d6c05c18174a4f46558))
* **causal:** keep live faults to the final stretch on long catch-up gaps ([a5731b2](https://github.com/rknightion/synthkit/commit/a5731b23ab8f24b11b7d590c97802201d0d9f7c8))
* **causal:** round arithmetic before digest-sensitive timestamp additions ([901adfd](https://github.com/rknightion/synthkit/commit/901adfd09497b37c96631452302ab75424ad62e1))
* **causal:** scope lambda and SQS consumer identities to account and region ([05a37a3](https://github.com/rknightion/synthkit/commit/05a37a3877e97364c7608d240894f284c002db86))
* **chatsim:** fresh struct per IRM incident page; correct avatar docs ([38600ce](https://github.com/rknightion/synthkit/commit/38600cec20f472abd5f932fa397b3ccfc46009f0))
* **chatsim:** re-wake after a director error, fire summons after the line posts, keep repeated edits ([b5901d4](https://github.com/rknightion/synthkit/commit/b5901d4ca6729de84ad44df781bcb808ae590c74))
* **chatsim:** refuse a seed block with no type before wiping ([aaf11f5](https://github.com/rknightion/synthkit/commit/aaf11f5ddb3fbf95d8787fee4eb6813831471b6c))
* **console:** keep SVG icons legible in the dark theme ([87f4be5](https://github.com/rknightion/synthkit/commit/87f4be5a867f0496ddf1d6af101e06f6990f2f57))
* **control:** make an explicit volume_multiplier of 0 pause emission ([94ed16b](https://github.com/rknightion/synthkit/commit/94ed16b325b91fd313c4d3497eec90c5a0f81d8a))
* **control:** quiet request-driven delivery lanes no longer fail readiness ([c8508cd](https://github.com/rknightion/synthkit/commit/c8508cd8bf30e23b21fef169820f1ade060b3e45))
* **deps:** invalidate stale skcapture certificate layers ([27455d6](https://github.com/rknightion/synthkit/commit/27455d63e60d1d644ae1104ca26d2d29eba3c3ff))
* **deps:** refresh chart image pin with patched Go and CA roots ([#210](https://github.com/rknightion/synthkit/issues/210)) ([0bb55c9](https://github.com/rknightion/synthkit/commit/0bb55c985c05c35f9e5458087f7335dda89ad81c))
* **deps:** require Go 1.27.3 and patched container trust stores ([78df722](https://github.com/rknightion/synthkit/commit/78df722de51f8ec69e10ddca7791765a4fdbe1eb))
* **deps:** update kubernetes libraries to v0.37.2 ([#247](https://github.com/rknightion/synthkit/issues/247)) ([73e3ecd](https://github.com/rknightion/synthkit/commit/73e3ecda824bece9739d384df23815aebcb2fabe))
* **deps:** update module github.com/grafana/grafana-foundation-sdk/go to v0.0.21 ([#222](https://github.com/rknightion/synthkit/issues/222)) ([224faac](https://github.com/rknightion/synthkit/commit/224faac8f80137542767f46d34b097aefb1abe45))
* **deps:** update module github.com/grafana/grafana-foundation-sdk/go to v0.0.22 ([#250](https://github.com/rknightion/synthkit/issues/250)) ([029b1cd](https://github.com/rknightion/synthkit/commit/029b1cd20afba19421f1436aeb247e52c9d0893c))
* **deps:** update module github.com/grafana/nanogit to v1.7.4 ([#231](https://github.com/rknightion/synthkit/issues/231)) ([80ba775](https://github.com/rknightion/synthkit/commit/80ba7757017e04ddb755c2145c10504cd39a449a))
* **deps:** update module github.com/slack-go/slack to v0.31.0 ([#242](https://github.com/rknightion/synthkit/issues/242)) ([37056a5](https://github.com/rknightion/synthkit/commit/37056a5626e263129801650a35c2afe6b9ab2709))
* **deps:** update module github.com/slack-go/slack to v0.32.0 ([#249](https://github.com/rknightion/synthkit/issues/249)) ([5029982](https://github.com/rknightion/synthkit/commit/5029982f889bd5e908fa3a3f766d23119c8a62c3))
* **deps:** update module github.com/slack-go/slack to v0.33.0 ([#254](https://github.com/rknightion/synthkit/issues/254)) ([820b936](https://github.com/rknightion/synthkit/commit/820b9360c4bf2f8d20ffadc475286d7dd6bfcca2))
* deterministically order failure-mode schema rows ([4b24a9f](https://github.com/rknightion/synthkit/commit/4b24a9fe4725868b78880972133ad10268e3c140))
* **docdb:** report bound pool connections on the writer only ([6040d6c](https://github.com/rknightion/synthkit/commit/6040d6c9bc51452437b8328e3b5e5edb81dbdded))
* enforce CW declaration ceiling across all loader routes ([0e25c2e](https://github.com/rknightion/synthkit/commit/0e25c2e03a130d19bfca0f00a6bbf6f8d9b64d64))
* **failuremode:** require a floor before a zero-baseline series counts as moved ([a70df05](https://github.com/rknightion/synthkit/commit/a70df05c1d4b1b1d73e1e75414439d62cf38e27f))
* **failuremode:** scope live lambda and SQS resource checks to account and region ([bb92dd8](https://github.com/rknightion/synthkit/commit/bb92dd816c84b12c2640d64b3a4ddfbaf39b7cb0))
* fence observed SQS pause resume boundary ([ea22785](https://github.com/rknightion/synthkit/commit/ea22785e2fb78f490b8554ac459fb4d5d0690354))
* gate bounded custody state capacity by revision ([34b27f6](https://github.com/rknightion/synthkit/commit/34b27f6916f0d30e14598bfeafe800e6bf3ecd7d))
* **incidents:** resolve a shared target name on the axis that declares the mode ([f8564d3](https://github.com/rknightion/synthkit/commit/f8564d302cda8ce566e0102c616bdd2aacfcbe28))
* **k8sprofiling:** push no cluster profiles at an explicit volume_multiplier of 0 ([da3176e](https://github.com/rknightion/synthkit/commit/da3176e7a2a1bf3e78722b19d79eacaedc48779e))
* **lambda:** include failed execution duration in bound metrics ([09e38d8](https://github.com/rknightion/synthkit/commit/09e38d8123f7481bb1963b9ff833eee8fef5ced7))
* **otlp:** keep span JSON identical when a span has no events; sort clock test labels ([11957e5](https://github.com/rknightion/synthkit/commit/11957e5ccbd77f786d1a79a4034dcc9b770224eb))
* preserve authored scheduled zero intensity ([f737cc3](https://github.com/rknightion/synthkit/commit/f737cc37c31cf6f397a4974aa09df3f86dda6368))
* preserve earned DynamoDB traffic carry across cadence gaps ([7573924](https://github.com/rknightion/synthkit/commit/7573924c31d51b1868dc4dfba1211f42aafd2b44))
* preserve exact-read client interruption causes ([63fccfe](https://github.com/rknightion/synthkit/commit/63fccfea90d76092e4008fb2f27ecfe6450bc7e4))
* reject non-finite incident intensity ([ff9248e](https://github.com/rknightion/synthkit/commit/ff9248e7e96f557a900ae341e51b0080764c436d))
* reject unsupported app pipeline output declarations ([c0982f0](https://github.com/rknightion/synthkit/commit/c0982f09ceca1b01ffb9e5220d0d592b35cc79d5))
* reserve custody capacity for canonical teardown ([a1caa1b](https://github.com/rknightion/synthkit/commit/a1caa1b01c1fad14f5204e46b9c0d42ec8b6f2b7))
* **runner:** check resource kinds and expand axis wildcards for live incidents ([1e1c205](https://github.com/rknightion/synthkit/commit/1e1c205e35b05fdd93d05fbc2989154605bc49f1))
* **selfobs:** bridge standard logs to current HA role provider ([627829e](https://github.com/rknightion/synthkit/commit/627829ec1f708796dd11542c124f1b9969535bb1))
* **sigil:** keep the thinking part kind when its content is empty ([bb31a00](https://github.com/rknightion/synthkit/commit/bb31a000e244be4c1e8522c2c1f1bbf90da7bf02))
* **solace:** preserve fractional queue traffic ([3d1e3d6](https://github.com/rknightion/synthkit/commit/3d1e3d6a67aea93e442f30f7fa890ce80c98cbb3))
* sort map-order iterations that made base output nondeterministic ([c27b3c1](https://github.com/rknightion/synthkit/commit/c27b3c19db8f4109c199f5db258d2c2ce0fee870))
* **sqs:** accept the documented load: constant default ([befb37b](https://github.com/rknightion/synthkit/commit/befb37ba924e5d3326bf0e2bba26468de4bfcc87))
* **synthkit:** bind the non-HA control listener before logging its address ([dea5f12](https://github.com/rknightion/synthkit/commit/dea5f123b2e1b7e4880f889eb0919d7b2007c082))
* **test:** retain renderer identity controls per supported architecture ([bcc5a6f](https://github.com/rknightion/synthkit/commit/bcc5a6fe6fdfb0d172f8e0eddb56bf2a8b586e3a))


### Performance

* **agento11yprovision:** size the custody budget with one encode per call ([c7bbb2b](https://github.com/rknightion/synthkit/commit/c7bbb2b88dd67290e9bedd02b8e9d7aff48a26db))
* **app:** derive only the deterministic correlation SpanID ([11a16be](https://github.com/rknightion/synthkit/commit/11a16bebef5052b933e7f401a7471f1d39e3fdfc))
* **causal:** cache non-legacy arrivals and cut timing compaction cost ([168ecfb](https://github.com/rknightion/synthkit/commit/168ecfbb11578ae411dabafefbfb5fde3539f1d8))
* **causal:** drop the oldest pending message by reslicing ([8007af2](https://github.com/rknightion/synthkit/commit/8007af203966071b57f07f1400d787ba4635aa82))
* compare custody trees without redundant serialization ([0810d95](https://github.com/rknightion/synthkit/commit/0810d95231dfb3d561e6642da97c9d4cadd8bf4f))
* compose exact bounded causal evaluator and lifecycle repair ([73ce017](https://github.com/rknightion/synthkit/commit/73ce017525ec10ce6f43e4c9c77550902267c567))
* **test:** reuse complete canonical parity metric bytes ([747b77a](https://github.com/rknightion/synthkit/commit/747b77a30c0ff0afafbc14af87d6900b6311aad3))


### Refactor

* **dynamodb:** use the shared table axis constant ([0d5fc25](https://github.com/rknightion/synthkit/commit/0d5fc25dee608abe1ca133fbe61757cf5fcec111))
* extract single-mutation initial bindings seam ([b8dcc3c](https://github.com/rknightion/synthkit/commit/b8dcc3cea4e088458c7fbe38f9deacc082d4307d))
* rename the SM helper without changing admission ([097743b](https://github.com/rknightion/synthkit/commit/097743b00341cf0248a0bf72d819d460763f69bd))


### Documentation

* **agentcore:** describe bound failed-invocation latency ([71921ba](https://github.com/rknightion/synthkit/commit/71921bacff675035010ba6f22471bfd50df0222e))
* **app:** generate synchronous invocation field contract ([d03ec4f](https://github.com/rknightion/synthkit/commit/d03ec4fb015e2e3f662534ed5ae72f8e302ff52a))
* **backlog:** close H100 exporter and record checkpoint increment ([d4b5943](https://github.com/rknightion/synthkit/commit/d4b594395ed7c687f608ca72ce480290552706f0))
* **backlog:** close loop50 remaining tasks ([dd9e529](https://github.com/rknightion/synthkit/commit/dd9e529fd87bd65f2052e27f3753b12233ef3c21))
* **backlog:** close validated fidelity and memory batch ([1bb6cac](https://github.com/rknightion/synthkit/commit/1bb6cac36cbf7b1ef07a9f74a630e8d4aaee6988))
* **backlog:** close verified HA operational log bridge ([343250c](https://github.com/rknightion/synthkit/commit/343250cfd40ab5328f9ef3ccbdf09a25d964070a))
* **backlog:** file control-dash race runtime task ([391d878](https://github.com/rknightion/synthkit/commit/391d8781aafad62c3a07163731051203507334bf))
* **backlog:** file dashboards push loop and flag docs task ([7e0a50c](https://github.com/rknightion/synthkit/commit/7e0a50c07cac1e2c8cf02bf6a5454b819110a463))
* **backlog:** file HA live-restore parity task ([5a98abc](https://github.com/rknightion/synthkit/commit/5a98abc28dbd47584411d887cd54195d460c0820))
* **backlog:** file live blueprint apply, startup kind disable and readiness flag tasks ([6a2fc97](https://github.com/rknightion/synthkit/commit/6a2fc975171fa2ded785652efd711377d29e688a))
* **backlog:** file live SM admission follow-up ([f96c4e7](https://github.com/rknightion/synthkit/commit/f96c4e725a6d187a4b71d1aecb211c41d0375bc6))
* **backlog:** file per-blueprint live SM admission task ([1972487](https://github.com/rknightion/synthkit/commit/1972487220ede6cf582c345277e43853c8113391))
* **backlog:** file SM cleanup after disable follow-up ([dcd9445](https://github.com/rknightion/synthkit/commit/dcd9445e6c9802a78fb1b394bccb86dc174cd83f))
* **backlog:** file the emission-only local run documentation task ([1cf0aaf](https://github.com/rknightion/synthkit/commit/1cf0aaf89096d8c1987b1243ded6f03a0ca0ffb9))
* **backlog:** file volume multiplier zero task ([85f794a](https://github.com/rknightion/synthkit/commit/85f794a04f3f91ab654e5e1ff5e23d0f204a1864))
* **backlog:** mark loop50 wave-2 tasks done ([141b745](https://github.com/rknightion/synthkit/commit/141b745ccf489209d838e7fd299472cc99860fc1))
* **backlog:** mark loop50 wave-3 tasks done ([8699249](https://github.com/rknightion/synthkit/commit/86992493c25d1dc773a9920e1fbde99b721c7851))
* **backlog:** mark the four loop50 engine tasks done ([9a01d43](https://github.com/rknightion/synthkit/commit/9a01d433fde30841a8630d10aa7b7931d11af223))
* **backlog:** park incomplete demo and capture at owner deadline ([635ca5d](https://github.com/rknightion/synthkit/commit/635ca5dcf47a58bcba2e293f8f19d5d95a4aac53))
* **backlog:** plan pharma regulatory authoring on current Agent Observability ([23aede2](https://github.com/rknightion/synthkit/commit/23aede2a611f04fc4225f21dc78fb17cbca112d4))
* **backlog:** record bounded capture cleanup and demo proof limits ([126104f](https://github.com/rknightion/synthkit/commit/126104f701ef2d59d2403da019668ca8d50a91c5))
* **backlog:** record checkpoint design and foundation boundary ([fe35e86](https://github.com/rknightion/synthkit/commit/fe35e8662b54e000d0d925387ae669542a74a87e))
* **backlog:** record operational and teardown capability gaps ([e91269e](https://github.com/rknightion/synthkit/commit/e91269ec4163c1ac70e84274e8c03f8085e774fc))
* **backlog:** record owner ruling on SKT-0147 volume 0 ([6268bec](https://github.com/rknightion/synthkit/commit/6268bec91fb76444fb7aa734c1258664f9eb64a8))
* **backlog:** redact verification deployment identifier ([6a0c6fd](https://github.com/rknightion/synthkit/commit/6a0c6fd8a10f39b9b5be36bae9618f21df157992))
* **dashboards:** push only dashboard JSON in the example loop ([6d8304a](https://github.com/rknightion/synthkit/commit/6d8304a74aab326fe76ced4603d47bed2175d1d7))
* describe agentcore and DocumentDB bindings and call-policy retries ([6b373f5](https://github.com/rknightion/synthkit/commit/6b373f5da7ddfdcefaedecff11d0c6a7d20460a7))
* describe bursts, scheduled drains and database bindings ([232aed3](https://github.com/rknightion/synthkit/commit/232aed37198298cba4a905b8b7e6c597e08d397f))
* distinguish closed runtime image identity paths ([4dc79d5](https://github.com/rknightion/synthkit/commit/4dc79d518292b363c9a23c005523a983f73d464b))
* distinguish Loki result metadata from indexed labels ([140835a](https://github.com/rknightion/synthkit/commit/140835aa68c045b3d0246210ebf12a9da1af5007))
* document the realism engine features ([13c7e22](https://github.com/rknightion/synthkit/commit/13c7e22b541140edef17299491cc8bc4a8655572))
* finalize loop task outcomes and honest remaining holds ([de11ef0](https://github.com/rknightion/synthkit/commit/de11ef0466ac2cb17c8cab6b9d11c8b2964618e2))
* freeze positive captured CloudWatch consumer contracts ([123b467](https://github.com/rknightion/synthkit/commit/123b46773aa8de821a3fbdea7872689fd6aa6cf5))
* **guide:** add an emission-only local run guide ([3af9667](https://github.com/rknightion/synthkit/commit/3af96670f13ad1361601fa3c5f1e5ed30dfe9ed4))
* point contributors to evaluator operating limits ([470b872](https://github.com/rknightion/synthkit/commit/470b8720a5fa31df151037ca16e95e3fc7f721e9))
* publish canonical agent documents ([e89df99](https://github.com/rknightion/synthkit/commit/e89df998550f96c38804ca1da43cd469dad1d2cd))
* publish canonical agent documents ([9c69518](https://github.com/rknightion/synthkit/commit/9c69518024058e4790ff75eadbb620428dc66e42))
* reconcile bounded app contracts and generated schema ([fafcdad](https://github.com/rknightion/synthkit/commit/fafcdad9b692eab9107c2b2dbc9ced82caa6d1fe))
* record baseline management scope and cleanup evidence ([ad418d0](https://github.com/rknightion/synthkit/commit/ad418d031b41ea6f201bd42b3db463e444bc9d1a))
* record bounded CW completions and delivery followup ([d096aca](https://github.com/rknightion/synthkit/commit/d096acad2a1fb6d136df1c11bc4a5dbe762fcbbf))
* record measured legacy evaluator cost and keep the ordered shape outside the envelope ([b72d6e2](https://github.com/rknightion/synthkit/commit/b72d6e212821cfa2a4b77d40b5805ebb05c9e604))
* record native profile source evidence boundary ([91dca83](https://github.com/rknightion/synthkit/commit/91dca8315daa998e4d9627f0c5782fc62b0a5ead))
* remove deployment-specific planning identifiers ([802deab](https://github.com/rknightion/synthkit/commit/802deabd549fa00c0c473ccfbaa882a23891adbc))
* restrict shared-legacy evaluation to measured offline use ([49b940f](https://github.com/rknightion/synthkit/commit/49b940f8aa1cb339cb7f6f82c34207e49661c755))
* retain non-clean closeout audit finding ([d6004e1](https://github.com/rknightion/synthkit/commit/d6004e1f7c1d52e9fa0137d5e644b277ebb7f4ea))
* **schema:** describe custom telemetry fields in the generated blueprint schema ([f9e2e3a](https://github.com/rknightion/synthkit/commit/f9e2e3aaa2bf126b9055b31c4d05d6c02c0f0981))
* scope resource wildcards to the mode's kind and note new evidence sources ([b05fee9](https://github.com/rknightion/synthkit/commit/b05fee941be2e37dd8e974eafe245f6d6794e477))
* **signals:** document causal spans, logs and the resource axis ([48ac577](https://github.com/rknightion/synthkit/commit/48ac577778d85e7a7a161365e0bf885751d34ca3))
* teach declared causes with a worked example and a fault catalogue ([4944338](https://github.com/rknightion/synthkit/commit/49443382d2baf43452ec0e5030d5a8b9f06f76b0))
* **traces:** qualify handled causal root status contract ([78356f9](https://github.com/rknightion/synthkit/commit/78356f92abe3a859c25ac9c40ae9fae3a165f484))
* warn against overlapping count_of histogram specs ([f111bb3](https://github.com/rknightion/synthkit/commit/f111bb3dc64a31b74d4553816d3d909f89efacc5))


### Build & CI

* **auto-rc:** only cut RCs for pushes from this repository ([86e489b](https://github.com/rknightion/synthkit/commit/86e489be8a83072cde95a6d735221470cc321ce2))
* clear the zizmor findings that fail fork pull requests ([fa254a8](https://github.com/rknightion/synthkit/commit/fa254a83c1e6f90771a1c564c69a443efaeb0f12))
* protect verified main image from edge cleanup ([2a0d861](https://github.com/rknightion/synthkit/commit/2a0d861e36d31fcb84e9c12a2940323b7f48e687))

## [1.5.1](https://github.com/rknightion/synthkit/compare/v1.5.0...v1.5.1) (2026-09-10)


### Bug Fixes

* **datadogreceiver:** ground system fixture values in captured baselines ([33f4cf4](https://github.com/rknightion/synthkit/commit/33f4cf42bd8ec615a2846fe6e31d888078b69459))
* **deps:** update module github.com/grafana/grafana-foundation-sdk/go to v0.0.19 ([#140](https://github.com/rknightion/synthkit/issues/140)) ([8351510](https://github.com/rknightion/synthkit/commit/83515101a689cfdef85a92cb9ecbb494ec56036c))
* **deps:** update module github.com/grafana/grafana-foundation-sdk/go to v0.0.20 ([#142](https://github.com/rknightion/synthkit/issues/142)) ([a949d9c](https://github.com/rknightion/synthkit/commit/a949d9cdef5e78d44bf82b88a29c91dded0aee00))


### Documentation

* **backlog:** reconcile receiver wave and precise capture attribution blocker ([7a2c5bc](https://github.com/rknightion/synthkit/commit/7a2c5bc21dd525e6435ef06bd9a9f974fbce2bf9))
* **datadogreceiver:** classify all standalone host capture families ([0ac777b](https://github.com/rknightion/synthkit/commit/0ac777b5fd87c14e836d496402844aa556968eea))

## [1.5.0](https://github.com/rknightion/synthkit/compare/v1.4.0...v1.5.0) (2026-09-09)


### Features

* **aiagent:** emit eval enqueue and judge cost ([2fd132c](https://github.com/rknightion/synthkit/commit/2fd132c7ca5e4601b88f7b80672674e5d656177b))
* complete P3 metrics and narrow conformance evidence ([edc985d](https://github.com/rknightion/synthkit/commit/edc985d89c94d505b9e519fb70943bebb13813cb))
* **corpus:** accept additive capture schema 2.1 ([e3dac54](https://github.com/rknightion/synthkit/commit/e3dac5415691d50d758329a9992a15a40a386f7b))
* **corpus:** integrate expanded control-plane series readback ([5fede23](https://github.com/rknightion/synthkit/commit/5fede235cd9c37aaf6bdb69ec28194536698439b))
* **corpus:** promote cumulative AWS capture evidence ([535d01b](https://github.com/rknightion/synthkit/commit/535d01ba2fad7cc42f3e4c74eaef48c9693ef76e))
* **corpus:** promote cumulative Azure capture evidence ([a18bf4d](https://github.com/rknightion/synthkit/commit/a18bf4dd6d2f1a8adc4560e60576a38a9114cd10))
* **corpus:** promote cumulative GCP capture evidence ([7a96ddb](https://github.com/rknightion/synthkit/commit/7a96ddbe6ee61d8a6d3a32a5ad0845286d4fd7f1))
* **corpus:** promote cumulative Rancher control-plane evidence ([75e7b79](https://github.com/rknightion/synthkit/commit/75e7b79b9a37590d585d6ec33ad6a43868d8587f))
* **corpus:** promote producer-attributed P3 captures ([1f759d8](https://github.com/rknightion/synthkit/commit/1f759d88485086acbf80ee301241e0cff9aa4cb6))
* **corpus:** retain reviewed capture limitations ([bbf663b](https://github.com/rknightion/synthkit/commit/bbf663b4b1309b997e411b21752c2434c0dfc4f4))
* **corpus:** select observed control-plane jobs for read-back ([5a6dd18](https://github.com/rknightion/synthkit/commit/5a6dd18f7675c3a4ca9ca13736b0e1e1c567c6f5))
* **fleet:** expose bounded configuration receipt evidence ([255e8f7](https://github.com/rknightion/synthkit/commit/255e8f7eba6ccd16275550724bcfe36c4a51f315))
* **inventory:** report producer-scoped identity coverage with a strict ratchet ([71ca8d4](https://github.com/rknightion/synthkit/commit/71ca8d452407c67adbaa5adaa32c6a7fbd667add))
* **k8s:** align control-plane telemetry with observations ([ad11bec](https://github.com/rknightion/synthkit/commit/ad11bec46f426d09e722cdab90843636a3d328d8))
* **k8s:** emit Collector envelopes and integrate capture tooling ([4dbe491](https://github.com/rknightion/synthkit/commit/4dbe4911c83d4bbae0835bf5e4d9df2233a54f61))
* model sourced Datadog system receiver families ([a62b439](https://github.com/rknightion/synthkit/commit/a62b439a905958d461e79aae812addc126d56777))
* select independently captured Datadog host receiver path ([ff701d7](https://github.com/rknightion/synthkit/commit/ff701d79495f9f48fdf497e17a2c9f640a2f684c))


### Bug Fixes

* **dump:** make fixed-tick trace inventory deterministic ([e51b1eb](https://github.com/rknightion/synthkit/commit/e51b1eb89be3aa3380eed7d6afd0730e808decef))
* **gate:** keep gitignored campaign scratch out of gofmt and drop a stack slug ([7cbafe0](https://github.com/rknightion/synthkit/commit/7cbafe0ea21513911dcf93a3c39b91f78e855447))
* **inventory:** compare attributed shapes and conditional metadata ([9426333](https://github.com/rknightion/synthkit/commit/94263330d2df3199a72813d8fa89854baa44e9a1))
* **inventory:** settle producer identity and ratchet unmatched claims ([ce93918](https://github.com/rknightion/synthkit/commit/ce93918c0fc750dbcf7cab001550eef69a363f87))
* **k8s:** match captured control-plane buckets and scrape labels ([a98aba0](https://github.com/rknightion/synthkit/commit/a98aba0dcdf0464f2c85ae1ce7b976b3cdffdc6c))
* **lab:** verify per-node image residency and retain bounded retry evidence ([d7e0281](https://github.com/rknightion/synthkit/commit/d7e0281ca0a9c88352eb023c3f143df47169ce0a))
* preserve captured P3 histogram bucket labels ([2b96494](https://github.com/rknightion/synthkit/commit/2b964945ddec8b4584091aafd0769dbf23519c75))
* resolve Azure and P3 envelopes and add Datadog receiver subset ([bcf1687](https://github.com/rknightion/synthkit/commit/bcf1687689fc9199e8f232b3cef23370b56f5aba))
* scope scanner exceptions to observed Datadog attribute names ([2c1703f](https://github.com/rknightion/synthkit/commit/2c1703f42b5d500bf9c9efb098ce67168f832136))


### Documentation

* **backlog:** file SKT-0053 and SKT-0054, record the Rancher promotion decisions ([42e3771](https://github.com/rknightion/synthkit/commit/42e3771e8de802626b8aeee6d5910e538c1cbdb1))
* **backlog:** file the corpus identity contract and the k3d import defect ([cebe724](https://github.com/rknightion/synthkit/commit/cebe724a46e40359c7f878a1dc6411533187aa5a))
* **backlog:** reconcile corpus and control-plane tasks ([5d1455d](https://github.com/rknightion/synthkit/commit/5d1455dc2d35a211e6504590dd165991028a7117))
* **backlog:** reconcile measured promotion and receiver boundaries ([cf6a0fe](https://github.com/rknightion/synthkit/commit/cf6a0febf633088ebd03984af80845d1cfff631b))
* **backlog:** reconcile release follow-up wave ([1750cf5](https://github.com/rknightion/synthkit/commit/1750cf53bc2595f3d3262071fded03afac6aa99e))
* **backlog:** start release follow-up wave ([6dd58eb](https://github.com/rknightion/synthkit/commit/6dd58eb6d19a6101080e5ac4c7428eabb6101c46))
* **backlog:** track migration rehearsal integration gaps ([10e7fa2](https://github.com/rknightion/synthkit/commit/10e7fa2518939c4e27a0dd4660fe42cd00e87514))
* close catalogue names and preserve typed P3 and Datadog captures ([429af6f](https://github.com/rknightion/synthkit/commit/429af6f234027ce254d5b4baff4d49d14c9d0ee3))
* close scanner finding with exact-SHA CI evidence ([db6d66d](https://github.com/rknightion/synthkit/commit/db6d66d4ee3f780a983bb0bb5f934d2da95452c5))
* reconcile evidence gaps and completed P3 scope ([b4b8711](https://github.com/rknightion/synthkit/commit/b4b87116d06a43b5cfd196c196359bfbeb337a01))
* record exact source and CI evidence for receiver and P3 tasks ([4a9348a](https://github.com/rknightion/synthkit/commit/4a9348a45955e0d6621aebe2d36cb403ce731cff))
* retain host receiver evidence and precise campaign parks ([bcdd624](https://github.com/rknightion/synthkit/commit/bcdd62409cfb72e22ce85e2191c24701c4bac80b))
* **signals:** correct the RW1 TYPE-metadata claim in the P3 provenance ([3fa1384](https://github.com/rknightion/synthkit/commit/3fa13844e72faac6dcd902944b607b6379703d6d))
* **signals:** name the exporter default that lost the P3 instrument types ([4eb4416](https://github.com/rknightion/synthkit/commit/4eb44160cea7f2b3a995872acf2730e98c9236d2))
* **skcapture:** pin stable 1.4.0 image ([e23acc2](https://github.com/rknightion/synthkit/commit/e23acc2c51af449866f676af1c0d8c7eedae4e05))
* sync authorised Astra root judgement ([a1f1288](https://github.com/rknightion/synthkit/commit/a1f128842860bfa0992156df735e4d4accef1c52))
* sync fan-out protocol, delegated root authority for unattended runs ([3e4c0b1](https://github.com/rknightion/synthkit/commit/3e4c0b1b29f41d4416052bc595bddf28aef85b5f))
* sync fan-out protocol, explicit add does not bound the commit ([4f0f0da](https://github.com/rknightion/synthkit/commit/4f0f0dae0af992a8e3b4ff961f07b1273447ce51))
* **tracker:** clarify retained control-plane label counts ([0240066](https://github.com/rknightion/synthkit/commit/0240066787a3b0d8777d4f4246f03785b5bd7825))
* **tracker:** reconcile producer identity and capture campaign ([887267a](https://github.com/rknightion/synthkit/commit/887267a81239ca65b87b7299fa630d364d7d4df3))

## [1.4.0](https://github.com/rknightion/synthkit/compare/v1.3.1...v1.4.0) (2026-09-06)


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
* **csp:** add opt-in vendor-native OTLP metric lanes ([c7fd022](https://github.com/rknightion/synthkit/commit/c7fd0225918446e5a65c5a878ec3233f0d81b77f))
* **cw:** verify Metric Streams lookups by namespace ([ade973f](https://github.com/rknightion/synthkit/commit/ade973f966ad4f693315a2f1d79d60faee7dc3c0))
* **envoy:** emit captured native OTLP metric contracts ([f9b1f56](https://github.com/rknightion/synthkit/commit/f9b1f56770b41b68c285ab7ea33d0fd2ab44ff89))
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
* **skcapture:** prove the Job and forge path in k3d ([b579863](https://github.com/rknightion/synthkit/commit/b5798633dcd431aa44cc0d586343359e34ac81b1))
* turn the k3d lab into a permutation matrix, and fix the Loki decode it exposed ([e534c58](https://github.com/rknightion/synthkit/commit/e534c58284a4a4532b651466b34669790a899b7e))


### Bug Fixes

* activate reviewed reality corpus ([8dc5d9b](https://github.com/rknightion/synthkit/commit/8dc5d9b078c1c201bfff4c915ba3ef0a54288c32))
* **aiagent:** match captured evaluation metric names and labels ([b091da6](https://github.com/rknightion/synthkit/commit/b091da6377de1a349555f919a7783dfbb36c2419))
* align e2e dump and receiver inventories ([25e8c04](https://github.com/rknightion/synthkit/commit/25e8c040967b9fb1793afba399bdcaca99f24929))
* align span metrics with trace outcomes ([30db2ba](https://github.com/rknightion/synthkit/commit/30db2baf71c9e4d03d5c2459806be677dca72aeb))
* **backlog:** correct SKT-0016 — the e2e failure is a subset-correlation regression ([23cf077](https://github.com/rknightion/synthkit/commit/23cf0772884a5b3a8cc46c54df7c354046209ab2))
* **backlog:** correct SKT-0018 — the live scheduler does reach the OTLP-logs lane ([894206b](https://github.com/rknightion/synthkit/commit/894206b322ca23702c31c3bd48a814dc9c445598))
* **blueprints:** the agent fleet in otlp-native was a test fixture, not a workload ([187b7f0](https://github.com/rknightion/synthkit/commit/187b7f030890a3f25d4b04e8a5a28850a3b6549a))
* **capture:** publish portable images and refuse unsupported forge assumptions ([63c183a](https://github.com/rknightion/synthkit/commit/63c183ac77ccf237fb76351cd5a27537a5910523))
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
* **k8s:** correct kubelet runtime operation labels ([52950f5](https://github.com/rknightion/synthkit/commit/52950f5b4e6425e01ee6a8cb406b7c4d3d70dcc4))
* **lab:** wait for the apiserver before the first kubectl apply ([e0f68e3](https://github.com/rknightion/synthkit/commit/e0f68e383021bfaf6db6e387cb601f619fe12fa1))
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
* **agents:** dashboards to AGENTS.md standard ([989d95a](https://github.com/rknightion/synthkit/commit/989d95ac7be160fe0981ded6bc0b40c705b7a4cf))
* **agents:** review fix synthkit root ([3f1e38f](https://github.com/rknightion/synthkit/commit/3f1e38f9d7c3a29dc561b5a97b333380c93a7639))
* **agents:** synthkit root to AGENTS.md standard ([7606207](https://github.com/rknightion/synthkit/commit/76062073242fd76dd96b84fd420e4830c2f23abb))
* **backlog:** close dump correlation repair with hosted proof ([1354975](https://github.com/rknightion/synthkit/commit/1354975330e4e9551c5c72e3313a9bd82565e890))
* **backlog:** close SKT-0006, unpark SKT-0007.09, check SKT-0007 AC1 ([6a6e489](https://github.com/rknightion/synthkit/commit/6a6e489bb6e8591ce24290aefb631490a97fa0fa))
* **backlog:** close SKT-0007 and SKT-0020.01, check SKT-0012 AC10 ([41326b1](https://github.com/rknightion/synthkit/commit/41326b1a73ecf2673e4474959cf932953f8334a3))
* **backlog:** close SKT-0012, the identity join holds on the cluster's real telemetry tenant ([693ec17](https://github.com/rknightion/synthkit/commit/693ec1759234d8180fb4585d12cf7ab9bf41ec90))
* **backlog:** close SKT-0018 ([1a1e0bf](https://github.com/rknightion/synthkit/commit/1a1e0bf3f3627bd6057accf09146201990a6f967))
* **backlog:** complete Go 1.27 upgrade ([cae239d](https://github.com/rknightion/synthkit/commit/cae239d8f501ae6754c7bc8d067a697b18e3b55f))
* **backlog:** file SKT-0010.21 and SKT-0012.10, record the SKT-0002 resume plan ([9330628](https://github.com/rknightion/synthkit/commit/93306284d3b6db61b5a5bcbead9fc57f82043389))
* **backlog:** file SKT-0012.11, release-tag publication of the skcapture image ([adcec0e](https://github.com/rknightion/synthkit/commit/adcec0efde704071a713381bd3c41d0cfc09c230))
* **backlog:** file SKT-0051 and SKT-0052 from the 2026-09-08 wave findings ([1ff7285](https://github.com/rknightion/synthkit/commit/1ff72857e27eb2c71c4dad4049211d72cef31398))
* **backlog:** park intermittent e2e failure ([29a6fac](https://github.com/rknightion/synthkit/commit/29a6facf91cec3728764dbb35c7b845a981c897c))
* **backlog:** reconcile campaign evidence ([f8b20d2](https://github.com/rknightion/synthkit/commit/f8b20d2b3e35d4fafb6dbddec5544dc4d5c983e2))
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
* reconcile cloud proofs and remaining capture boundaries ([ceeed36](https://github.com/rknightion/synthkit/commit/ceeed36897cc4eab3ad933d2139d2d074456e620))
* reconcile final Rancher and heuristic capture evidence ([10b3ba4](https://github.com/rknightion/synthkit/commit/10b3ba4eaae31f06a1495b275aa9ad4bc44cd543))
* reconcile live capture and campaign evidence ([b1e2534](https://github.com/rknightion/synthkit/commit/b1e25341975a4ea7fad215f76bb4ea24873e6d3d))
* record automatic prerelease verification for both images ([f8b4713](https://github.com/rknightion/synthkit/commit/f8b47134d658b066c99fd78e5a81c78eb1a1fe78))
* record final parked boundaries ([f45dec2](https://github.com/rknightion/synthkit/commit/f45dec2eda016d89a66f5b5c839d80095feb0d28))
* record lab verification boundaries ([4b7a80a](https://github.com/rknightion/synthkit/commit/4b7a80ae45261b892fe9c45037fceecd8f0971e2))
* record operational readiness closeout ([77c380d](https://github.com/rknightion/synthkit/commit/77c380dfa6c2bec8720529a0171c030beb1cab00))
* resolve SK-75's CloudWatch half and record the observed span resource ([60d99d5](https://github.com/rknightion/synthkit/commit/60d99d5c7f7e0c2853e777cab58afe2dc63a326e))
* scrub deployment host from task notes ([2a4732f](https://github.com/rknightion/synthkit/commit/2a4732f9d8c94ad88f74660ac370d5b964848f5c))
* settle agent observability contract ([2ffcaca](https://github.com/rknightion/synthkit/commit/2ffcacabf6fc316128dacbc861098ca0a759bf36))
* show the runtime control plane in the README ([ceb0b4a](https://github.com/rknightion/synthkit/commit/ceb0b4ab7a82672d8c028d0fe287bf61af1380ea))
* **sigil:** record final evaluation counter evidence ([aed0588](https://github.com/rknightion/synthkit/commit/aed05889be1cdacfddafb9896daa5fe56238ef64))
* **signals:** capture the Envoy Gateway OTLP surface, close SKT-0008.02 ([0ec5812](https://github.com/rknightion/synthkit/commit/0ec581209493428bc06b6bb7ab7a2a20c73164cb))
* **signals:** capture the full Envoy Gateway OTLP contract ([705f7c0](https://github.com/rknightion/synthkit/commit/705f7c02285bba6cf60f67d37c72ebb9f07cb598))
* **signals:** record the Beyla native ingestion readback ([b46922f](https://github.com/rknightion/synthkit/commit/b46922f809e013ff20df0ff4fe70f4e51c8e79a1))
* **signals:** resolve SK-86, capture the Envoy Gateway control-plane OTLP form ([f4224c1](https://github.com/rknightion/synthkit/commit/f4224c11f611cdf02d06d7bee8084b95c66e5234))
* **signals:** resolve the Google half of SK-88 from receiver source ([bee7e2a](https://github.com/rknightion/synthkit/commit/bee7e2ab5dc7099f5bac195d8436fb58a79d0859))
* **skcapture:** pin the published Job image and document retrieval ([b7f6c23](https://github.com/rknightion/synthkit/commit/b7f6c23e789b8799dde1848f51722b36ddcc5d96))
* sync agent-docs, a wave's launch message is a file not a chat block ([f19f4a7](https://github.com/rknightion/synthkit/commit/f19f4a727dcd5b8efa4152a458dc8fb11f5c3410))
* sync Astra routing and default wave reports to files ([f4f31dd](https://github.com/rknightion/synthkit/commit/f4f31dd94ec3cb3f199a7b9c792abcb6e3b06bdc))
* sync fan-out protocol from agent-docs ad7abd2 ([23fd100](https://github.com/rknightion/synthkit/commit/23fd10014b3ed2d2b251ad6ba615a46d60689f3c))
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
* prepare release publication and verification for capture image ([f7bb4a2](https://github.com/rknightion/synthkit/commit/f7bb4a275994dd3c0a228a35c88a56cf1497ac09))
* **publish:** publish the capture image through the shared reusable ([eba2318](https://github.com/rknightion/synthkit/commit/eba2318e49f8dd7b0f18911119355f8408311349))
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
