# DEC-0008：以租户为归属重建可插拔支付工作流

Status: Proposed
Date: 2026-09-01
Review owner: Project owner

## 提案状态与审批边界

本文是等待项目所有者审核的支付专项方案，不是已经接受的架构决策。文中的
“建议”“推荐”和目标模型均不授权实现、迁移数据、启用写接口或部署；尤其不能据此
写入生产数据库、修改生产 Secret 或接收真实支付流量。

只有项目所有者明确确认“需要审核的决策”后，才能把本文改为 `Accepted`，并据此新增
支付 Feature、权限迁移、业务代码、数据迁移和验收证据。未确认项不得被状态文档或
实现清单表述为既定要求。

本提案遵守已接受的 DEC-0002、DEC-0003 和 DEC-0007：商城运行时固定绑定一个租户和
一组 schema；前台只调用 `/app/v1`；存量表通过限定 schema 合同读取，在独立工作流
合格前保持只读，并且禁止长期双写。

## 背景与本轮业务边界

项目所有者要求首版支付只保留以下能力，同时保留以后快速接入新支付方式的扩展性：

1. 凭证支付；
2. 充值；
3. 金豆支付；
4. 微信在线支付；
5. 支付宝在线支付。

这五个名称在旧系统中并不处于同一个抽象层级。旧代码中的“充值”是管理员给余额或
金豆账户记账，不是一个 `Payment.Method`；金豆是内部资产；凭证是需要人工确认的
收款流程；微信和支付宝才是外部在线支付通道。如果继续把它们都当成可互换的字符串
方法，新系统会重复旧系统的状态歧义和安全问题。因此，本提案先明确术语，再提出
目标模型，最后列出仍需所有者确认的业务选择。

## 术语：五种能力必须分开表达

| 名称 | 建议的业务含义 | 不应被误解为 |
| --- | --- | --- |
| 凭证支付 | 租户展示收款账户或二维码，客户提交转账凭证，授权财务人员审核后确认收款 | 上传图片即支付成功；某一个固定的微信/支付宝 Provider |
| 充值 | 给钱包资产增加余额的业务目的或管理员调整动作；资金来源可为人工确认、微信或支付宝 | 一个外部支付 Provider；与余额支付自动等价 |
| 金豆支付 | 使用会员金豆账户完成全部或部分订单付款的内部支付工具 | 法定货币；外部网关；无流水的字段扣减 |
| 微信在线 | 通过一个已注册的微信 Provider Adapter 发起并由验签回调确认的在线支付 | 任意名称含 `Wechat` 的旧方法都可自动继续使用 |
| 支付宝在线 | 通过一个已注册的支付宝 Provider Adapter 发起并由验签回调确认的在线支付 | 任意名称含 `Alipay` 的旧方法都可自动继续使用 |

“余额支付”是否仍属于首版，以及“充值”是否包含客户自助充值，尚未得到确认，必须按
本文末尾 D-01 审核，不能从旧菜单名称自行推断。

## 旧系统证据与不能照搬的问题

### 数据和入口

支付与钱包相关的存量合同已经登记在
[`legacy-tables.yaml`](../migration/legacy-tables.yaml)：

- `payments`：全局支付模板，保存名称、任意 `method` 字符串、类型和终端 CSV；
- `payment_installs`：租户安装记录及 `app_key`、`app_secret`、收款图和说明；
- `payment_orders`：支付金额、金豆、外部单号、状态、凭证和回调标记；
- `orders`：支付名称、支付单、金豆、凭证及订单状态快照；
- `finances`、`finance_logs`：余额、金豆、冻结值和流水；
- `gold_withdraws`：金豆提现及打款凭证；
- `goods.payment_ids`、`shipping_warehouses.payment_ids` 和
  `member_levels.payment_ids`：旧支付可用范围。

旧后台支付定义、租户安装、钱包充值及提现接口见
`shop-r1/shop-go:app/shop/router.go:177-182,259-264,302-308,328-332`；前台支付单、凭证、
金豆和回调入口见同文件 `427-466`。旧 Admin 页面直接编辑并回显
`app_key/app_secret`，见
`shop-r1/shop-admin-ui:src/views/show-payment/payments.vue:83-103,175-189`。

### 状态和流程问题

1. `payment_orders` 只有 `pending/success/already/failed`；`already` 同时表示余额或
   金豆已完成以及客户已上传凭证，无法表达“待财务审核”。证据：
   `shop-r1/shop-go:app/shop/models/payment_order.go:10-17`。
2. 凭证只检查字符串是否含 `http`，上传后立即把支付单和订单标记为已付款，没有审核
   人、审核原因或驳回重提状态。证据：
   `shop-r1/shop-go:app/shop/controllers/payment_order.go:217-306`。
3. 公共回调把支付单、租户、会员和 token 放进路径，但处理器没有验证 Provider 签名、
   实付金额、币种或商户身份，收集的 token 也没有用于查询条件；随后直接确认付款。
   进程内互斥锁不能保证多 Pod 幂等。证据：
   `shop-r1/shop-go:app/shop/router.go:465-466` 和
   `app/shop/controllers/payment_order.go:325-380`。
4. `PaymentInstall` 把凭据作为普通 JSON 字段；读取缺少租户条件，前台列表返回完整模型，
   更新流程还可能先读取其他租户记录再保存。证据：
   `shop-r1/shop-go:app/shop/models/payment.go:66-77`、
   `app/shop/service/payment/payment_install.go:78-125` 和
   `app/shop/controllers/order_app.go:1730-1784`。
5. 支付派发依赖一个全局 `map[string]func` 和大量字符串 `switch`。后台可录入任意
   `method`，未知方法不会可靠地拒绝创建；历史值还存在拼写不一致。证据：
   `shop-r1/shop-go:tools/method.go:28-46` 和
   `shop-r1/shop-m-cli:src/pages/pay/index.vue:456-537`。
6. 某些 Provider 使用全局可变客户端配置，存在跨租户串配置风险；部分请求把签名参数
   放在 GET URL，缺少统一超时和脱敏日志策略；部分回调 URL 被强制为 `http`。证据：
   `shop-r1/shop-go:tools/method.go:131-205`、
   `tools/pay/omipay/pay.go:54-69,142-160` 和
   `app/shop/controllers/payment_order.go:143-148`。
7. 余额和金豆用 `float64` 承载，若干更新只按 `member_id`，缺少租户条件、行锁、非负
   约束和幂等键。部分冻结流水被注释，因此不能假设旧流水之和等于当前余额。证据：
   `shop-r1/shop-go:common/models/finance.go:5-17`、
   `app/shop/service/payment/finance.go:61-114,258-301` 和
   `app/shop/controllers/gold_withdraw.go:283-321`。
8. 后台所谓“取消充值”只是再提交一笔相反金额并把原流水号写入备注，没有结构化引用
   原流水。证据：
   `shop-r1/shop-admin-ui:src/views/finance-manage/balance.vue:444-475`。
9. 金豆与在线支付组合时只检查可用值，外部支付完成后才扣金豆，没有预占，存在并发
   超用窗口。证据：
   `shop-r1/shop-go:app/shop/service/payment/payment_order.go:104-135,204-217` 和
   `app/shop/service/order/order_app.go:163-193`。
10. 金豆提现不应混进支付 Provider。旧提现审核还存在无租户条件查询和错误 Model 查询，
    必须独立评审。证据：
    `shop-r1/shop-go:app/shop/controllers/gold_withdraw.go:117-162,249-280`。

这些问题是需要重新设计的理由，不是待复刻行为。当前
[`legacy-admin-acceptance-matrix.md`](../migration/legacy-admin-acceptance-matrix.md)
也已明确禁止复制密钥回显、空页面、前端鉴权和通用 CRUD 等旧缺陷。

## 建议的职责与租户归属

以下是等待审核的建议方向：

| 组件 | 建议职责 | 明确禁止 |
| --- | --- | --- |
| Provider capability catalog | 随应用版本发布已审查的 Provider code、能力、配置版本和支持场景；平台可设置全局可用策略 | 在线编辑函数名、上传任意运行时代码、保存租户商户密钥 |
| 商城平台 | 租户维护自己的支付实例、凭据引用、收款说明、启停、排序、终端场景；查看支付单、审核凭证、管理钱包 | 选择其他租户或 schema；把密钥作为普通详情字段返回 |
| Storefront API | 创建和查询支付意图、提交凭证、处理微信/支付宝 webhook、向 H5/小程序返回稳定合同 | 暴露 MSS Admin API；相信客户端提供的租户、金额或回调成功状态 |
| Worker | 超时释放、主动查单、失败重试、订单付款 outbox 消费和对账任务 | 依赖进程内锁保证一次执行 |
| Reconciler | 建立 tenant schema、数据库权限、运行资源和 Secret 引用 | 由 HTTP handler 直接创建 schema 或 Kubernetes Secret |

推荐由平台代码定义“系统支持哪些 Provider 能力”，由租户在自己的商城平台配置和启用
实例。旧的全局 `payments` 表只作为迁移输入，不再决定函数派发。该建议尚需 D-08
确认。

## 建议的目标模型

下列名称表达职责，不承诺最终表名；接受 ADR 后仍需在 Feature 和迁移设计中确定物理
合同。

| 聚合/记录 | 关键职责 |
| --- | --- |
| `PaymentMethodConfig` | 租户支付实例、稳定 method/provider code、支持场景、公开展示配置、SecretRef、配置版本、启停和 legacy ID |
| `PaymentIntent` | 一次业务付款目标；保存 purpose、业务单据、应付金额/币种、状态、幂等键、版本和到期时间 |
| `PaymentAttempt` | 一次 Provider 或人工支付尝试；保存外部单号、请求金额、渠道状态和安全摘要 |
| `PaymentAllocation` | 金豆、余额、凭证或在线支付在同一意图中的金额分摊与占用/确认状态 |
| `PaymentEventInbox` | 已验签回调或主动查单事件；Provider 事件 ID/交易号唯一，记录摘要与处理结果 |
| `VoucherEvidence` | 受保护对象 key、内容 hash、媒体类型、提交人、审核状态、审核人/时间/原因 |
| `WalletAccount` | 每会员、每资产的账户；CNY 余额、AUD 余额和金豆互不混用 |
| `WalletEntry` | 不可变借贷/变动流水，引用业务单据、原流水、幂等键和迁移批次 |
| `WalletReservation` | 组合支付、提现或其他未完成业务对金豆/余额的预占、确认与释放 |
| `OutboxEvent` | 与支付状态同事务写入，可靠推进订单、通知和异步对账 |

租户隔离主要由每租户固定业务 schema 提供，但每个仓储操作仍应携带不可变租户上下文
并通过限定仓储边界；不能因为已拆 schema 就移除领域归属校验。

## 建议的状态机

### 支付意图

```text
pending
  |-- voucher submitted ----------> requires_review
  |-- online order created --------> processing
  |-- fully covered by gold -------> succeeded
  |-- validation/provider failure -> failed
  |-- user/system cancellation ----> cancelled
  `-- timeout ---------------------> expired

requires_review -- approve -------> succeeded
                `-- reject -------> pending or failed

processing -- verified callback/query --> succeeded
           |-- provider failure -------> failed
           |-- cancellation -----------> cancelled
           `-- timeout ----------------> expired
```

只有 `succeeded` 可以产生幂等的 `OrderPaid` outbox 事件。状态推进必须使用带当前状态和
版本条件的事务更新；非法重复推进应返回稳定结果或冲突，不得产生第二份资金或订单
效果。

### 凭证支付

1. 创建 intent 和 voucher attempt，返回租户公开的收款说明；
2. 客户上传受保护对象，服务端验证大小、类型和内容 hash；
3. intent 进入 `requires_review`，订单仍未确认付款；
4. 具备专门后端权限的财务人员批准或驳回，记录操作者、原因和审计；
5. 批准事务写入成功状态和 outbox；驳回是否允许重提由 Feature 明确。

凭证对象不能只靠 URL 字符串判断，也不能使用永久公开地址。后台预览使用短时签名
URL，响应和日志不暴露存储凭据。

### 微信/支付宝在线支付

1. 在调用 Provider 前持久化 intent/attempt 和商户配置版本；
2. Provider Adapter 以固定商户配置创建外部订单；
3. webhook 使用原始 body 和 headers 验签，并核对商户、外部单号、金额、币种和时间窗；
4. inbox 以 Provider 事件 ID 或交易号建立唯一约束；
5. 同一事务更新 attempt/intent 并写出 outbox；
6. Worker 主动查单补偿丢失回调，得到的状态进入同一幂等处理函数。

回调 URL 不再包含可作为认证依据的会员 ID 或 token。租户只能由服务端维护的精确
Host/AppID/商户绑定解析，客户端不能指定 schema。回跳地址必须使用允许列表。

### 金豆与组合支付

纯金豆付款可在一个数据库事务内完成。金豆加微信、支付宝或凭证时，先创建
`WalletReservation`：

- 外部部分成功或凭证批准后，原子确认 reservation 并写入金豆流水；
- 外部失败、取消或超时后，幂等释放 reservation；
- 不允许只检查余额而延迟到回调后无条件扣减。

### 充值

推荐把充值建模为 `purpose=wallet_recharge`，而不是 Provider：

- 管理员人工充值是一个受权限、原因和幂等键约束的钱包调整工作流；
- 客户自助充值若被接受，可使用微信或支付宝 attempt，只有在线支付成功后才记钱包
  流水；
- 使用钱包余额支付订单是另一种内部支付工具，不能因为有充值流程就自动进入首版。

该拆分是推荐答案，仍需 D-01 明确首版到底包含哪几项。

## Provider 扩展能力

建议采用编译期注册、能力分离的 Provider，而不是继续使用任意字符串到函数的全局
映射。接口形状示意如下，最终签名由 Feature 和 Go 实现评审确定：

```go
type OnlineProvider interface {
	Code() ProviderCode
	Descriptor() ProviderDescriptor
	ValidateConfig(ctx context.Context, config PublicConfig, secrets SecretReader) error
	CreatePayment(ctx context.Context, request CreatePaymentRequest) (CreatePaymentResult, error)
	VerifyWebhook(ctx context.Context, rawBody []byte, headers http.Header) (VerifiedEvent, error)
	QueryPayment(ctx context.Context, request QueryPaymentRequest) (ProviderPayment, error)
}

type ClosableProvider interface {
	ClosePayment(ctx context.Context, request ClosePaymentRequest) error
}

type RefundProvider interface {
	CreateRefund(ctx context.Context, request CreateRefundRequest) (RefundResult, error)
}
```

- `ClosePayment`、`CreateRefund` 等属于可选 capability，核心流程不得假设所有 Provider
  都支持；
- Registry 在启动时拒绝重复或未知 code，并把能力版本纳入 readiness；
- 每个调用使用当前租户支付实例构造无共享可变状态的 client；
- Provider 配置使用版本化、类型化 DTO，不能把原始 JSON 字典交给通用编辑器；
- 新增 Provider 的改动限定在 adapter、注册、配置 DTO/界面、webhook verifier、双语消息
  和合同验证，不修改支付意图状态机；
- 不建议 Go 运行时动态插件。应用版本化发布已经能快速、安全地接入新方式，并保留
  可审查、可回滚和可重复构建能力。该建议需 D-11 确认。

凭证和金豆是内部策略，不伪装成外部 `OnlineProvider`。它们与在线 Provider 共享支付
意图、分摊、幂等、审计和 outbox，而不是共享不适用的 webhook 方法。

## 安全、幂等与金额不变量

若本提案被接受，后续 Feature 至少要证明以下不变量：

1. 商户密钥只通过只写 DTO 进入；数据库仅保存 SecretRef 或经批准的应用层密文。
   读取显示掩码/指纹，日志、错误、审计和前台响应不含明文；轮换产生配置版本，不改写
   历史交易使用的版本。
2. webhook 必须验签并核对商户、订单、金额、币种和时间窗。网络来源或路径 token
   不能替代验签。
3. 创建 intent 使用 `(tenant, purpose, business_reference, idempotency_key)` 唯一约束；
   Provider 事件、钱包流水、凭证审核和订单付款各有自己的唯一业务键。
4. 支付状态、钱包确认和 outbox 在一个事务内提交；外部调用不可与数据库事务混成一个
   长事务，失败通过可重试状态收敛。
5. 所有法币和金豆都使用明确 scale 的定点十进制或最小单位整数；禁止 `float64`。
   金豆不是法币但仍要规定精度。汇率使用定点十进制并在交易中保存快照。
6. 钱包流水不可修改和删除；冲正必须创建引用原流水的新 entry。账户现值可由期初项
   和不可变流水重算，并满足非负/冻结约束。
7. 每个后台动作使用独立 MSS 权限并在后端校验，例如支付设置读取/管理、支付单读取、
   凭证审核、钱包读取和钱包调整。菜单或按钮可见性不构成授权。
8. 上传凭证使用受保护对象、大小和媒体类型限制、内容 hash、短时预览 URL 和访问审计。
9. HTTP client 统一设置超时、TLS、重试边界和脱敏观测；不得打印支付 URL、签名、
   请求原文中的秘密或 Provider 完整错误对象。
10. 新页面遵循 mss-boot-admin Admin Web 的布局、令牌、表格、表单、反馈和状态模式；
    使用聚焦、类型化页面，不扩展通用只读查看器为支付万能编辑器，并同时提供
    `zh-CN`/`en-US` 消息。

## 存量数据转换与对账建议

迁移目标是保留旧数据语义和可追溯性，不是证明旧实现安全。建议步骤如下：

1. 在已有 `r1shop-dev` 数据上做只读盘点；任何写入演练都使用隔离开发副本或集群内
   一次性 Pod。不得把生产订单复制到开发环境。
2. 记录各表/租户行数、主键、索引、约束、软删除、distinct method/type/status/terminal、
   引用孤儿、金额合计和稳定样本 hash。报告只包含计数/摘要，不输出凭据。
3. `payments + payment_installs` 转为租户 `PaymentMethodConfig`，保留
   `legacy_payment_id`、`legacy_install_id` 和旧 Provider code：
   - `*Voucher`、`OfflinePay`、`CopyVoucher` 归入凭证语义；
   - `GoldPay` 归入金豆；
   - `Overage` 暂归钱包余额候选，是否启用取决于 D-01；
   - 历史微信/支付宝方法只归类为语义通道，不能仅凭名字认定新 Provider 凭据兼容。
4. 旧密钥只能由受控迁移程序写入新的 Secret 存储；验收记录存在性、版本和不可逆
   指纹，不记录值。不能转换的实例保持禁用，由租户重新录入。
5. `payment_orders + orders` 转为 intent、attempt 和 allocation，保留旧 ID、原 method、
   原状态和迁移批次：
   - `pending -> pending`；
   - `success -> succeeded`，但标记来源为 legacy；
   - `failed -> failed`；
   - `already` 按 method 拆分：金豆/余额可在金额对账后映射成功；凭证只能标记
     `legacy_unverified`。历史完成订单不被反向改写，仍活跃的凭证单进入人工处置清单。
6. `finances + finance_logs` 不能假设天然平衡。把可验证旧流水导入后，为每个账户创建
   有迁移批次和原因的 opening/migration adjustment，使新账本精确等于旧账户快照；
   单独报告差额，禁止静默改旧数据。
7. 旧 `payment_ids` CSV 规范化为关联关系；`h5/mp/app` 规范化为 scene。App 首期停用，
   但保留历史值。
8. 每次导入记录 source hash、映射版本、创建/跳过/失败数、状态分布、金额总和和孤儿
   关系，保证重复执行不产生第二份结果。
9. 通过限定仓储合同切换一次，不做永久双写。兼容 view 或旧仓储只保留到行数、hash、
   关系和金额对账通过，并有明确删除条件。

## 建议的首版范围

以下范围是推荐答案，不是已批准计划：

### 建议纳入首版

- 租户支付方式设置：凭证、微信在线、支付宝在线和金豆相关策略；
- 支付意图、尝试、分摊、状态查询、事件 inbox 和 outbox；
- 凭证提交、财务审核、驳回/重提和受保护预览；
- 微信/支付宝 H5 与微信小程序所需场景、验签 webhook、主动查单和超时收敛；
- 纯金豆及金豆加一种外部支付方式的组合支付和 reservation；
- 钱包/金豆账户、不可变流水、管理员充值和引用式冲正；客户自助充值是否纳入取决于
  D-01；
- 支付单、审核和钱包账本的 MSS 风格后台页面、后端权限、审计及双语；
- 存量数据转换、状态/金额对账和只读历史查询。

### 建议延期或另行评审

- 原生 App 支付；首期仅 H5 和微信小程序；
- 旧 RoyalPay、SandPay、OmiPay、SuperPay、Paylinx 等所有变体原样复活；只有 D-02
  确认的 Provider 才接收新交易；
- 运行时动态插件、在线录入函数名或任意 Provider 脚本；
- 多个外部 Provider 拆单、分期、代扣/订阅、预授权、拒付和争议处理；
- 金豆提现/打款。它属于 payout，建议历史只读并暂停新申请，等待独立风险评审；
- 自动在线退款。旧退款页面为空，不能作为已存在功能；接受退款状态机前，已付款取消
  必须被阻断或进入明确的人工退款工单；
- 第三种语言和 App 专属场景。

## 需要项目所有者审核的决策

以下每一项在得到明确答案前都保持未确认：

| 编号 | 待确认问题 | 推荐答案 | 未确认时的处理 |
| --- | --- | --- | --- |
| D-01 | “充值”具体包含什么：管理员人工余额入账、余额支付、客户用微信/支付宝自助充值？ | 三者分开建模；首版保留管理员人工充值，自助充值复用在线 Provider；余额支付只有明确需要时才启用 | 不实现余额支付或客户自助充值，只保留模型位置和旧数据 |
| D-02 | 微信/支付宝新交易直连官方渠道，还是继续使用某个旧聚合网关？ | 新流量优先官方 Provider；旧网关默认只保留历史，若实际商户合同要求再单独批准一个 adapter | 不迁移或启用任何旧网关凭据 |
| D-03 | 客户上传凭证后是否必须由财务审核？ | 必须审核，通过后才支付成功；驳回保留原因并允许受控重提 | 不开放凭证写流程 |
| D-04 | 是否保留“金豆 + 凭证/微信/支付宝”组合支付？ | 保留，但必须先 reservation，再在外部成功或凭证批准时确认 | 只允许纯金豆或纯外部支付 |
| D-05 | 是否继续保留 CNY/AUD 双余额和旧汇率语义？ | 历史和账本保留双资产，不静默换算；新在线能力按已确认 Provider 支持范围启用 | 只读保留无法处理的币种，不接受新交易 |
| D-06 | 首版是否继续金豆提现？ | 不属于支付首版；保留历史只读，暂停新申请，后续作为 payout 独立评审 | 不创建提现写接口 |
| D-07 | 已付款订单取消采用自动退款、人工退款工单，还是禁止？ | 自动退款状态机接受前，采用明确人工退款工单；不能仅改订单状态 | 阻断已付款取消 |
| D-08 | 支付方式由平台还是租户维护？ | 平台代码发布 Provider capability 和全局策略；租户维护自己的支付实例、启停、展示和凭据 | 不删除旧全局 payments 读取面，也不开放新配置写入 |
| D-09 | 凭证支付是否允许多个命名收款账户？ | 允许一个租户配置多个实例，共用 voucher strategy，并按仓库/商品/会员规则选择 | 默认只设计一个实例，不迁移多账户规则 |
| D-10 | 旧 `already` 凭证记录如何定性？ | 历史完成订单保持业务结果但标记 `legacy_unverified`；活跃单进入人工处置，不伪造审核记录 | 不自动映射为新系统审核通过 |
| D-11 | 新 Provider 是版本化编译发布还是运行时动态插件？ | 版本化编译注册；一次新增局限在 adapter 和配置表面，经过 CI 后发版 | 禁止动态加载代码或任意 method 函数名 |

审核时可以逐项接受推荐答案，也可以为某项给出替代答案。任何替代答案若改变固定租户、
密钥边界、回调验签、金额精度或生产只读要求，还需要单独安全评审。

## 接受后才产生的后续工作

若本 ADR 被接受，实施仍需独立完成以下工作，不能因 ADR 通过就宣称业务已恢复：

1. 新增支付/钱包 Feature 规格，定义最终实体、API、状态和错误键；
2. 建立手写业务模块、固定 schema 仓储、MSS 菜单/API 权限前向迁移和 readiness；
3. 实现双语 MSS 风格聚焦页面，不修改 mss-boot-admin 核心；
4. 为每一个旧 API/按钮建立新操作、兼容适配或批准的退役映射；
5. 在隔离开发副本执行迁移与对账，在集群一次性 Pod 完成系统验证；
6. 分别记录允许/拒绝权限、跨租户负例、回调重放、金额并发、凭证审核和迁移重跑证据；
7. 另行取得开发部署、阶段验收以及任何生产动作的授权。

## 被本提案否定的替代方向

- 原样复制旧 `PaymentMethod` 字符串、全局函数 map 和前端大 switch；
- 用通用 MSS CRUD 直接编辑支付单、钱包、密钥或回调状态；
- 把上传凭证视为付款成功；
- 依靠 URL token、IP 白名单或进程 mutex 代替 Provider 验签和数据库幂等；
- 用 `float64`、可修改流水或负数充值维持资金账户；
- 为兼容旧表永久双写一套新表；
- 让客户端选择租户、schema、应付金额或成功状态；
- 为“快速扩展”牺牲版本化构建、代码审查和可回滚性。

## 当前结果

`Proposed` 状态下，本 ADR 只形成审核材料。当前 `payments`、`payment_installs`、
`payment_orders`、`finances`、`finance_logs` 和 `gold_withdraws` 兼容资源继续保持只读；
现有 31 个业务验收场景不会因此关闭。
