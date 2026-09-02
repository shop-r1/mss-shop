import {
  SHARED_CATALOG_FIELDS,
  SHARED_CATALOG_RESOURCES,
  type SharedCatalogFieldName,
  type SharedCatalogResourceName,
} from '../shared-catalog/catalog';

const resourceTitles: Record<SharedCatalogResourceName, string> = {
  payments: 'Payment Methods',
};

const fieldTitles: Record<SharedCatalogFieldName, string> = {
  id: 'ID',
  created_at: 'Created At',
  updated_at: 'Updated At',
  deleted_at: 'Deleted At',
  logo: 'Logo',
  site_url: 'Website',
  description: 'Description',
  status: 'Status',
  name: 'Name',
  method: 'Method',
  type: 'Type',
  terminals: 'Terminal Configuration',
};

const messages: Record<string, string> = {
  'modules.tenant.actions.confirmDelete':
    'Archive this control-plane record? This does not destroy tenant resources.',
  'modules.tenant.actions.delete': 'Archive',
  'modules.tenant.messages.deleteSuccess': 'Archived successfully',
  'sharedCatalog.domain': 'Platform Payment Catalog',
  'menu.sharedCatalog.domain': 'Platform Payment Catalog',
  'menu.sharedCatalog': 'Platform Payment Catalog',
  'sharedCatalog.value.yes': 'Yes',
  'sharedCatalog.value.no': 'No',
  'sharedCatalog.table.actions': 'Actions',
  'sharedCatalog.action.view': 'View',
  'sharedCatalog.action.edit': 'Edit',
  'sharedCatalog.action.cancel': 'Cancel',
  'sharedCatalog.action.delete': 'Delete',
  'sharedCatalog.action.refresh': 'Refresh',
  'sharedCatalog.action.create': 'Create',
  'sharedCatalog.action.save': 'Save',
  'sharedCatalog.delete.title': 'Delete this shared catalog record?',
  'sharedCatalog.delete.description':
    'This may affect every tenant. Confirm that the record is no longer in use.',
  'sharedCatalog.feedback.deleted': 'Deleted successfully',
  'sharedCatalog.feedback.created': 'Created successfully',
  'sharedCatalog.feedback.updated': 'Updated successfully',
  'sharedCatalog.error.unknownResource': 'The requested shared catalog resource was not found.',
  'sharedCatalog.error.forbidden':
    'You do not have permission to access or change this shared catalog resource.',
  'sharedCatalog.error.unavailableTitle': 'Shared catalog service unavailable',
  'sharedCatalog.error.unavailable':
    'The shared catalog schema is not ready or the service is temporarily unavailable. Try again later.',
  'sharedCatalog.error.loadTitle': 'Unable to load data',
  'sharedCatalog.error.load': 'The data could not be loaded. Try again later.',
  'sharedCatalog.empty': 'No matching records',
  'sharedCatalog.pagination.total': '{total} records',
  'sharedCatalog.search.submit': 'Search',
  'sharedCatalog.search.placeholder': 'Search this resource by name, ID, or keyword',
  'sharedCatalog.detail.title': 'Record details: {id}',
  'sharedCatalog.editor.createTitle': 'Create {title}',
  'sharedCatalog.editor.editTitle': 'Edit {title}',
  'sharedCatalog.editor.noWritableFields': 'This resource has no editable fields.',
  'sharedCatalog.validation.required': 'Enter {field}',
  'sharedCatalog.validation.json': 'Enter valid JSON',
  'sharedCatalog.secret.keep': 'Leave blank to keep the current value',
  'sharedCatalog.secret.enter': 'Enter the sensitive value',
  'sharedCatalog.readOnly.title': 'This resource is currently read-only',
  'sharedCatalog.readOnly.generic':
    'This resource has legacy relationship side effects. Direct writes remain disabled until a dedicated workflow is available.',
  'sharedCatalog.readOnly.brands':
    'Brand changes affect the global product catalogue and cached storefront projections. This resource is currently read-only.',
  'sharedCatalog.readOnly.categories':
    'Category changes trigger legacy hierarchy and relationship synchronization. This resource is read-only to preserve those hooks.',
  'sharedCatalog.readOnly.classes':
    'Product class changes affect shared attributes and product structures. This resource is read-only until an audited workflow is available.',
  'sharedCatalog.readOnly.goods_infos':
    'Standard product changes trigger product relationship and snapshot synchronization. This resource is currently read-only.',
  'sharedCatalog.readOnly.couriers':
    'Courier changes affect global shipping integrations and tenant fulfilment. Direct editing is currently disabled.',
  'sharedCatalog.readOnly.courier_pack_rules':
    'Courier packing rules affect global shipping and packing calculations. This resource is read-only until a dedicated workflow is available.',
  'sharedCatalog.readOnly.courier_links':
    'Global courier links carry cross-rule relationship side effects. Direct editing is disabled.',
  'sharedCatalog.readOnly.payments':
    'Payment method changes affect global checkout integrations. This resource is read-only until a dedicated workflow is available.',
  'sharedCatalog.errors.authenticationRequired': 'Your session has expired. Sign in again.',
  'sharedCatalog.errors.forbidden':
    'You do not have permission to perform this shared catalog action.',
  'sharedCatalog.errors.authorizationUnavailable':
    'The authorization service is unavailable. Try again later.',
  'sharedCatalog.errors.resourceNotFound': 'The requested shared catalog resource was not found.',
  'sharedCatalog.errors.recordNotFound':
    'The shared catalog record was not found or has been deleted.',
  'sharedCatalog.errors.conflict':
    'The shared catalog data has changed or conflicts with another record. Refresh and try again.',
  'sharedCatalog.errors.validationFailed': 'The submitted data did not pass validation.',
  'sharedCatalog.errors.schemaNotReady': 'The shared catalog schema is not ready. Try again later.',
  'sharedCatalog.errors.operationNotSupported':
    'This shared catalog resource does not support that operation.',
  'sharedCatalog.errors.invalidRequest': 'The request parameters are invalid.',
  'sharedCatalog.errors.internal':
    'The shared catalog service could not complete the request. Try again later.',
  'sharedCatalog.errors.requestFailed': 'The shared catalog request failed. Try again later.',
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
