import {
  SHARED_CATALOG_FIELDS,
  SHARED_CATALOG_RESOURCES,
  type SharedCatalogFieldName,
  type SharedCatalogResourceName,
} from '../shared-catalog/catalog';

const resourceTitles: Record<SharedCatalogResourceName, string> = {
  payments: '支付方式',
};

const fieldTitles: Record<SharedCatalogFieldName, string> = {
  id: '编号',
  created_at: '创建时间',
  updated_at: '更新时间',
  deleted_at: '删除时间',
  logo: '标志',
  site_url: '官方网站',
  description: '描述',
  status: '状态',
  name: '名称',
  method: '方式',
  type: '类型',
  terminals: '终端配置',
};

const messages: Record<string, string> = {
  'modules.tenant.actions.confirmDelete': '归档这条控制面记录？此操作不会销毁租户资源。',
  'modules.tenant.actions.delete': '归档',
  'modules.tenant.messages.deleteSuccess': '归档成功',
  'sharedCatalog.domain': '平台支付目录',
  'menu.sharedCatalog.domain': '平台支付目录',
  'menu.sharedCatalog': '平台支付目录',
  'sharedCatalog.value.yes': '是',
  'sharedCatalog.value.no': '否',
  'sharedCatalog.table.actions': '操作',
  'sharedCatalog.action.view': '查看',
  'sharedCatalog.action.edit': '编辑',
  'sharedCatalog.action.cancel': '取消',
  'sharedCatalog.action.delete': '删除',
  'sharedCatalog.action.refresh': '刷新',
  'sharedCatalog.action.create': '新增',
  'sharedCatalog.action.save': '保存',
  'sharedCatalog.delete.title': '确认删除这条共享目录记录？',
  'sharedCatalog.delete.description': '该操作可能影响所有租户，请确认记录不再被使用。',
  'sharedCatalog.feedback.deleted': '删除成功',
  'sharedCatalog.feedback.created': '新增成功',
  'sharedCatalog.feedback.updated': '更新成功',
  'sharedCatalog.error.unknownResource': '未找到对应的共享目录资源。',
  'sharedCatalog.error.forbidden': '你没有访问或操作该共享目录资源的权限。',
  'sharedCatalog.error.unavailableTitle': '共享目录服务暂不可用',
  'sharedCatalog.error.unavailable': '共享目录数据结构尚未就绪或服务暂不可用，请稍后重试。',
  'sharedCatalog.error.loadTitle': '加载失败',
  'sharedCatalog.error.load': '数据加载失败，请稍后重试。',
  'sharedCatalog.empty': '暂无符合条件的数据',
  'sharedCatalog.pagination.total': '共 {total} 条',
  'sharedCatalog.search.submit': '搜索',
  'sharedCatalog.search.placeholder': '按名称、编号或关键字搜索当前资源',
  'sharedCatalog.detail.title': '记录详情：{id}',
  'sharedCatalog.editor.createTitle': '新增{title}',
  'sharedCatalog.editor.editTitle': '编辑{title}',
  'sharedCatalog.editor.noWritableFields': '该资源没有可编辑字段。',
  'sharedCatalog.validation.required': '请输入{field}',
  'sharedCatalog.validation.json': '请输入有效的 JSON',
  'sharedCatalog.secret.keep': '留空表示保持原值',
  'sharedCatalog.secret.enter': '请输入敏感字段值',
  'sharedCatalog.readOnly.title': '此资源当前仅支持查看',
  'sharedCatalog.readOnly.generic': '该资源涉及旧系统关系副作用，完成专用业务流程前禁止直接写入。',
  'sharedCatalog.readOnly.brands': '品牌变更会影响全局商品目录与商城缓存投影；当前仅允许查看。',
  'sharedCatalog.readOnly.categories':
    '分类变更会触发旧系统的层级与关联同步；当前仅允许查看，避免绕过历史业务钩子。',
  'sharedCatalog.readOnly.classes':
    '商品类别变更会影响共享属性与商品结构；审计后的专用流程完成前仅允许查看。',
  'sharedCatalog.readOnly.goods_infos':
    '标准商品资料变更会触发商品关联与快照同步；当前仅允许查看。',
  'sharedCatalog.readOnly.couriers':
    '物流服务商变更会影响全局物流集成与租户履约；当前禁止直接编辑。',
  'sharedCatalog.readOnly.courier_pack_rules':
    '物流包装规则会参与全局运费与包装计算；专用变更流程完成前仅允许查看。',
  'sharedCatalog.readOnly.courier_links':
    '物流全局关联包含跨规则关系副作用；当前仅允许查看，不提供直接编辑。',
  'sharedCatalog.readOnly.payments':
    '支付方式变更会影响全局结算集成；专用变更流程完成前仅允许查看。',
  'sharedCatalog.errors.authenticationRequired': '登录状态已失效，请重新登录。',
  'sharedCatalog.errors.forbidden': '你没有执行该共享目录操作的权限。',
  'sharedCatalog.errors.authorizationUnavailable': '权限服务暂不可用，请稍后重试。',
  'sharedCatalog.errors.resourceNotFound': '请求的共享目录资源不存在。',
  'sharedCatalog.errors.recordNotFound': '共享目录记录不存在或已被删除。',
  'sharedCatalog.errors.conflict': '共享目录数据已发生变化或存在冲突，请刷新后重试。',
  'sharedCatalog.errors.validationFailed': '提交的数据未通过校验，请检查后重试。',
  'sharedCatalog.errors.schemaNotReady': '共享目录数据结构尚未就绪，请稍后重试。',
  'sharedCatalog.errors.operationNotSupported': '当前共享目录资源不支持该操作。',
  'sharedCatalog.errors.invalidRequest': '请求参数无效，请检查后重试。',
  'sharedCatalog.errors.internal': '共享目录服务处理失败，请稍后重试。',
  'sharedCatalog.errors.requestFailed': '共享目录请求失败，请稍后重试。',
};

for (const entry of SHARED_CATALOG_RESOURCES) {
  const title = resourceTitles[entry.resource];
  messages[entry.titleKey] = title;
  messages[`menu.${entry.resource}`] = title;
  messages[`menu.${entry.menuName}`] = title;
}

for (const field of SHARED_CATALOG_FIELDS) {
  messages[`legacy.fields.${field}`] = fieldTitles[field];
}

export default messages;
