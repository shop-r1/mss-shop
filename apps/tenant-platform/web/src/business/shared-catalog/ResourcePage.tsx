import {
  DeleteOutlined,
  EditOutlined,
  EyeOutlined,
  PlusOutlined,
  ReloadOutlined,
} from '@ant-design/icons';
import {
  getRequestStatus,
  hasPermission,
  type InitialState,
  PageContainer,
  PageEmpty,
  PageError,
  PageForbidden,
  PageLoading,
} from '@mss-boot-io/admin-web/runtime';
import { useIntl, useLocation, useModel } from '@umijs/max';
import {
  Alert,
  App,
  Button,
  Descriptions,
  Drawer,
  Form,
  Input,
  InputNumber,
  Modal,
  Pagination,
  Popconfirm,
  Result,
  Space,
  Switch,
  Table,
  type TableColumnsType,
  Tag,
  Typography,
} from 'antd';
import type { ReactNode } from 'react';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { sharedCatalogClient } from './api';
import { findSharedCatalogResourceByPath, type SharedCatalogResource } from './catalog';
import { localizeSharedCatalogError } from './errors';
import {
  isJSONColumn,
  isValidJSONText,
  recordID,
  toFormValues,
  toMutationPayload,
  visibleColumns,
  writableColumns,
} from './record';
import type { SharedCatalogColumn, SharedCatalogListResponse, SharedCatalogRecord } from './types';

interface RequestFailure {
  message: string;
  status?: number;
}

interface DetailState {
  open: boolean;
  loading: boolean;
  id?: string;
  record?: SharedCatalogRecord;
  error?: RequestFailure;
}

interface EditorState {
  open: boolean;
  loading: boolean;
  saving: boolean;
  mode: 'create' | 'edit';
  id?: string;
  error?: RequestFailure;
}

const initialDetail: DetailState = { open: false, loading: false };
const initialEditor: EditorState = {
  open: false,
  loading: false,
  saving: false,
  mode: 'create',
};

function requestFailure(error: unknown, formatError: (error: unknown) => string): RequestFailure {
  return {
    message: formatError(error),
    status: getRequestStatus(error),
  };
}

function humanize(value: string): string {
  return value.replace(/_/g, ' ').replace(/\b\w/g, (character) => character.toUpperCase());
}

function renderValue(value: unknown, booleanLabels: { yes: string; no: string }): ReactNode {
  if (value === null || value === undefined || value === '') {
    return <Typography.Text type="secondary">—</Typography.Text>;
  }
  if (typeof value === 'boolean') {
    return (
      <Tag color={value ? 'green' : 'default'}>{value ? booleanLabels.yes : booleanLabels.no}</Tag>
    );
  }
  const text = typeof value === 'object' ? JSON.stringify(value) : String(value);
  return (
    <Typography.Text ellipsis={{ tooltip: text }} style={{ maxWidth: 320 }}>
      {text}
    </Typography.Text>
  );
}

function renderDetailValue(value: unknown, booleanLabels: { yes: string; no: string }): ReactNode {
  if (value !== null && typeof value === 'object') {
    const text = JSON.stringify(value, null, 2);
    return (
      <Typography.Paragraph copyable={{ text }} style={{ marginBottom: 0 }}>
        <pre
          style={{
            margin: 0,
            maxHeight: 320,
            overflow: 'auto',
            whiteSpace: 'pre-wrap',
          }}
        >
          {text}
        </pre>
      </Typography.Paragraph>
    );
  }
  return renderValue(value, booleanLabels);
}

function fieldControl(column: SharedCatalogColumn, secretPlaceholder: string) {
  const type = column.type.toLowerCase();
  if (column.secret || type === 'secret') {
    return <Input.Password autoComplete="new-password" placeholder={secretPlaceholder} />;
  }
  if (type === 'boolean' || type === 'bool') return <Switch />;
  if (
    type === 'number' ||
    type === 'integer' ||
    type === 'int' ||
    type === 'decimal' ||
    type === 'float'
  ) {
    return (
      <InputNumber
        precision={type === 'integer' || type === 'int' ? 0 : undefined}
        style={{ width: '100%' }}
      />
    );
  }
  if (isJSONColumn(column) || type === 'text' || type === 'longtext') {
    return <Input.TextArea autoSize={{ minRows: isJSONColumn(column) ? 6 : 3, maxRows: 14 }} />;
  }
  if (type === 'date') return <Input type="date" />;
  if (type === 'datetime' || type === 'timestamp') {
    return <Input type="datetime-local" />;
  }
  return <Input />;
}

function SharedCatalogResourcePage() {
  const intl = useIntl();
  const location = useLocation();
  const { message } = App.useApp();
  const { initialState } = useModel('@@initialState') as {
    initialState?: InitialState;
  };
  const user = initialState?.currentUser;
  const entry = findSharedCatalogResourceByPath(location.pathname);
  const [form] = Form.useForm<SharedCatalogRecord>();
  const formatError = useCallback(
    (error: unknown) =>
      localizeSharedCatalogError(error, (id, params) => intl.formatMessage({ id }, params)),
    [intl],
  );

  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [searchInput, setSearchInput] = useState('');
  const [query, setQuery] = useState('');
  const [list, setList] = useState<SharedCatalogListResponse>();
  const [loading, setLoading] = useState(false);
  const [listError, setListError] = useState<RequestFailure>();
  const [detail, setDetail] = useState<DetailState>(initialDetail);
  const [editor, setEditor] = useState<EditorState>(initialEditor);
  const [deletingID, setDeletingID] = useState<string>();

  const canRoute = Boolean(entry && hasPermission(user, entry.routePermission));
  const canList = Boolean(entry && canRoute && hasPermission(user, entry.listPermission));

  const loadList = useCallback(async () => {
    if (!entry || !canList) return;
    setLoading(true);
    setListError(undefined);
    try {
      setList(
        await sharedCatalogClient.list(entry, {
          page,
          pageSize,
          q: query || undefined,
        }),
      );
    } catch (error) {
      setListError(requestFailure(error, formatError));
    } finally {
      setLoading(false);
    }
  }, [canList, entry, formatError, page, pageSize, query]);

  useEffect(() => {
    void loadList();
  }, [loadList]);

  const descriptor = list?.resource;
  const booleanLabels = useMemo(
    () => ({
      yes: intl.formatMessage({ id: 'sharedCatalog.value.yes' }),
      no: intl.formatMessage({ id: 'sharedCatalog.value.no' }),
    }),
    [intl],
  );

  const canRead = Boolean(
    entry &&
      descriptor?.capabilities.detail &&
      canRoute &&
      hasPermission(user, entry.readPermission),
  );
  const canCreate = Boolean(
    entry?.writable &&
      descriptor?.capabilities.create &&
      canRoute &&
      hasPermission(user, entry.createPermission),
  );
  const canUpdate = Boolean(
    entry?.writable &&
      descriptor?.capabilities.update &&
      canRoute &&
      hasPermission(user, entry.updatePermission),
  );
  const canDelete = Boolean(
    entry?.writable &&
      descriptor?.capabilities.delete &&
      canRoute &&
      hasPermission(user, entry.deletePermission),
  );

  const labelFor = useCallback(
    (column: SharedCatalogColumn) =>
      intl.formatMessage({
        id: column.label,
        defaultMessage: humanize(column.name),
      }),
    [intl],
  );

  const openDetail = useCallback(
    async (resource: SharedCatalogResource, id: string) => {
      setDetail({ open: true, loading: true, id });
      try {
        const record = await sharedCatalogClient.detail(resource, id);
        setDetail({ open: true, loading: false, id, record });
      } catch (error) {
        setDetail({
          open: true,
          loading: false,
          id,
          error: requestFailure(error, formatError),
        });
      }
    },
    [formatError],
  );

  const openCreate = useCallback(() => {
    form.resetFields();
    setEditor({ open: true, loading: false, saving: false, mode: 'create' });
  }, [form]);

  const openEdit = useCallback(
    async (resource: SharedCatalogResource, id: string) => {
      form.resetFields();
      setEditor({
        open: true,
        loading: true,
        saving: false,
        mode: 'edit',
        id,
      });
      try {
        const record = await sharedCatalogClient.detail(resource, id);
        form.setFieldsValue(toFormValues(descriptor?.columns ?? [], record));
        setEditor({
          open: true,
          loading: false,
          saving: false,
          mode: 'edit',
          id,
        });
      } catch (error) {
        setEditor({
          open: true,
          loading: false,
          saving: false,
          mode: 'edit',
          id,
          error: requestFailure(error, formatError),
        });
      }
    },
    [descriptor?.columns, form, formatError],
  );

  const removeRecord = useCallback(
    async (resource: SharedCatalogResource, id: string) => {
      setDeletingID(id);
      try {
        await sharedCatalogClient.remove(resource, id);
        message.success(intl.formatMessage({ id: 'sharedCatalog.feedback.deleted' }));
        await loadList();
      } catch (error) {
        message.error(formatError(error));
      } finally {
        setDeletingID(undefined);
      }
    },
    [formatError, intl, loadList, message],
  );

  const saveRecord = useCallback(
    async (values: SharedCatalogRecord) => {
      if (!entry?.writable || !descriptor) return;
      setEditor((current) => ({ ...current, saving: true, error: undefined }));
      try {
        const payload = toMutationPayload(descriptor.columns, values);
        if (editor.mode === 'create') {
          await sharedCatalogClient.create(entry, payload);
        } else if (editor.id) {
          await sharedCatalogClient.update(entry, editor.id, payload);
        }
        message.success(
          intl.formatMessage({
            id:
              editor.mode === 'create'
                ? 'sharedCatalog.feedback.created'
                : 'sharedCatalog.feedback.updated',
          }),
        );
        setEditor(initialEditor);
        form.resetFields();
        await loadList();
      } catch (error) {
        setEditor((current) => ({
          ...current,
          saving: false,
          error: requestFailure(error, formatError),
        }));
      }
    },
    [descriptor, editor.id, editor.mode, entry, form, formatError, intl, loadList, message],
  );

  const tableColumns = useMemo<TableColumnsType<SharedCatalogRecord>>(() => {
    if (!entry || !descriptor) return [];
    const columns: TableColumnsType<SharedCatalogRecord> = visibleColumns(descriptor.columns).map(
      (column) => ({
        title: labelFor(column),
        dataIndex: column.name,
        key: column.name,
        ellipsis: true,
        render: (value: unknown) => renderValue(value, booleanLabels),
      }),
    );

    if (canRead || canUpdate || canDelete) {
      columns.push({
        title: intl.formatMessage({ id: 'sharedCatalog.table.actions' }),
        key: '__actions',
        fixed: 'right',
        width: 190,
        render: (_value: unknown, record: SharedCatalogRecord) => {
          const id = recordID(record);
          return (
            <Space size="small">
              {canRead ? (
                <Button
                  aria-label={intl.formatMessage({
                    id: 'sharedCatalog.action.view',
                  })}
                  disabled={!id}
                  icon={<EyeOutlined />}
                  onClick={() => id && void openDetail(entry, id)}
                  size="small"
                  type="link"
                />
              ) : null}
              {canUpdate ? (
                <Button
                  aria-label={intl.formatMessage({
                    id: 'sharedCatalog.action.edit',
                  })}
                  disabled={!id}
                  icon={<EditOutlined />}
                  onClick={() => id && void openEdit(entry, id)}
                  size="small"
                  type="link"
                />
              ) : null}
              {canDelete ? (
                <Popconfirm
                  cancelText={intl.formatMessage({
                    id: 'sharedCatalog.action.cancel',
                  })}
                  description={intl.formatMessage({
                    id: 'sharedCatalog.delete.description',
                  })}
                  disabled={!id}
                  okButtonProps={{
                    danger: true,
                    loading: Boolean(id && deletingID === id),
                  }}
                  okText={intl.formatMessage({
                    id: 'sharedCatalog.action.delete',
                  })}
                  onConfirm={() => id && removeRecord(entry, id)}
                  title={intl.formatMessage({
                    id: 'sharedCatalog.delete.title',
                  })}
                >
                  <Button
                    aria-label={intl.formatMessage({
                      id: 'sharedCatalog.action.delete',
                    })}
                    danger
                    disabled={!id}
                    icon={<DeleteOutlined />}
                    loading={Boolean(id && deletingID === id)}
                    size="small"
                    type="link"
                  />
                </Popconfirm>
              ) : null}
            </Space>
          );
        },
      });
    }
    return columns;
  }, [
    booleanLabels,
    canDelete,
    canRead,
    canUpdate,
    deletingID,
    descriptor,
    entry,
    intl,
    labelFor,
    openDetail,
    openEdit,
    removeRecord,
  ]);

  if (!entry) {
    return (
      <Result
        status="404"
        subTitle={intl.formatMessage({
          id: 'sharedCatalog.error.unknownResource',
        })}
        title="404"
      />
    );
  }

  if (!canRoute || !canList) {
    return <PageForbidden message={intl.formatMessage({ id: 'sharedCatalog.error.forbidden' })} />;
  }

  const catalogTitle = intl.formatMessage({ id: entry.titleKey });
  const title = intl.formatMessage({
    id: descriptor?.titleKey ?? entry.titleKey,
    defaultMessage: catalogTitle,
  });
  const description = intl.formatMessage({ id: entry.domainTitleKey });
  const detailColumns = descriptor ? visibleColumns(descriptor.columns) : [];
  const formColumns = descriptor ? writableColumns(descriptor.columns) : [];

  let content: ReactNode;
  if (listError?.status === 403) {
    content = <PageForbidden message={listError.message} />;
  } else if (listError) {
    const unavailable = listError.status === 503;
    content = (
      <PageError
        message={listError.message}
        onRetry={() => void loadList()}
        title={intl.formatMessage({
          id: unavailable
            ? 'sharedCatalog.error.unavailableTitle'
            : 'sharedCatalog.error.loadTitle',
        })}
      />
    );
  } else if (!list && loading) {
    content = <PageLoading rows={7} />;
  } else if (list && list.data.length === 0) {
    content = <PageEmpty description={intl.formatMessage({ id: 'sharedCatalog.empty' })} />;
  } else {
    content = (
      <>
        <Table<SharedCatalogRecord>
          columns={tableColumns}
          dataSource={list?.data ?? []}
          loading={loading}
          pagination={false}
          rowKey={(record) =>
            recordID(record) ?? `row-${Math.max(0, list?.data.indexOf(record) ?? 0)}`
          }
          scroll={{ x: 'max-content' }}
          size="middle"
        />
        <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 16 }}>
          <Pagination
            current={page}
            onChange={(nextPage, nextPageSize) => {
              setPage(nextPageSize !== pageSize ? 1 : nextPage);
              setPageSize(nextPageSize);
            }}
            pageSize={pageSize}
            pageSizeOptions={[10, 20, 50, 100]}
            showSizeChanger
            showTotal={(total) =>
              intl.formatMessage({ id: 'sharedCatalog.pagination.total' }, { total })
            }
            total={list?.total ?? 0}
          />
        </div>
      </>
    );
  }

  return (
    <PageContainer content={description} title={title}>
      <Space orientation="vertical" size="large" style={{ width: '100%' }}>
        {!entry.writable ? (
          <Alert
            description={intl.formatMessage({
              id: entry.readOnlyReasonKey ?? 'sharedCatalog.readOnly.generic',
            })}
            message={intl.formatMessage({
              id: 'sharedCatalog.readOnly.title',
            })}
            showIcon
            type="info"
          />
        ) : null}
        <Space wrap style={{ display: 'flex', justifyContent: 'space-between' }}>
          <Input.Search
            allowClear
            enterButton={intl.formatMessage({
              id: 'sharedCatalog.search.submit',
            })}
            onChange={(event) => setSearchInput(event.target.value)}
            onSearch={(value) => {
              setPage(1);
              setQuery(value.trim());
            }}
            placeholder={intl.formatMessage({
              id: 'sharedCatalog.search.placeholder',
            })}
            style={{ maxWidth: 480 }}
            value={searchInput}
          />
          <Space>
            <Button icon={<ReloadOutlined />} loading={loading} onClick={() => void loadList()}>
              {intl.formatMessage({ id: 'sharedCatalog.action.refresh' })}
            </Button>
            {canCreate ? (
              <Button icon={<PlusOutlined />} onClick={openCreate} type="primary">
                {intl.formatMessage({ id: 'sharedCatalog.action.create' })}
              </Button>
            ) : null}
          </Space>
        </Space>
        {content}
      </Space>

      <Drawer
        destroyOnHidden
        onClose={() => setDetail(initialDetail)}
        open={detail.open}
        title={intl.formatMessage({ id: 'sharedCatalog.detail.title' }, { id: detail.id ?? '' })}
        size={720}
      >
        {detail.loading ? <PageLoading rows={6} /> : null}
        {detail.error ? (
          detail.error.status === 403 ? (
            <PageForbidden message={detail.error.message} />
          ) : (
            <Alert message={detail.error.message} showIcon type="error" />
          )
        ) : null}
        {!detail.loading && !detail.error && detail.record ? (
          <Descriptions bordered column={1} size="small">
            {detailColumns.map((column) => (
              <Descriptions.Item key={column.name} label={labelFor(column)}>
                {renderDetailValue(detail.record?.[column.name], booleanLabels)}
              </Descriptions.Item>
            ))}
          </Descriptions>
        ) : null}
      </Drawer>

      <Modal
        cancelText={intl.formatMessage({ id: 'sharedCatalog.action.cancel' })}
        confirmLoading={editor.saving}
        destroyOnHidden
        mask={{ closable: false }}
        okButtonProps={{ disabled: editor.loading || formColumns.length === 0 }}
        okText={intl.formatMessage({ id: 'sharedCatalog.action.save' })}
        onCancel={() => {
          setEditor(initialEditor);
          form.resetFields();
        }}
        onOk={() => form.submit()}
        open={editor.open}
        title={intl.formatMessage(
          {
            id:
              editor.mode === 'create'
                ? 'sharedCatalog.editor.createTitle'
                : 'sharedCatalog.editor.editTitle',
          },
          { title },
        )}
        width={760}
      >
        {editor.loading ? <PageLoading rows={6} /> : null}
        {editor.error ? (
          <Alert
            message={editor.error.message}
            showIcon
            style={{ marginBottom: 16 }}
            type="error"
          />
        ) : null}
        {!editor.loading ? (
          formColumns.length > 0 ? (
            <Form<SharedCatalogRecord>
              form={form}
              layout="vertical"
              onFinish={(values) => void saveRecord(values)}
            >
              {formColumns.map((column) => {
                const required = column.required && !(column.secret && editor.mode === 'edit');
                const label = labelFor(column);
                return (
                  <Form.Item
                    key={column.name}
                    label={label}
                    name={column.name}
                    rules={[
                      {
                        required,
                        message: intl.formatMessage(
                          { id: 'sharedCatalog.validation.required' },
                          { field: label },
                        ),
                      },
                      ...(isJSONColumn(column)
                        ? [
                            {
                              validator: (_rule: unknown, value: unknown) =>
                                isValidJSONText(value)
                                  ? Promise.resolve()
                                  : Promise.reject(
                                      new Error(
                                        intl.formatMessage({
                                          id: 'sharedCatalog.validation.json',
                                        }),
                                      ),
                                    ),
                            },
                          ]
                        : []),
                    ]}
                    valuePropName={
                      column.type === 'boolean' || column.type === 'bool' ? 'checked' : 'value'
                    }
                  >
                    {fieldControl(
                      column,
                      editor.mode === 'edit'
                        ? intl.formatMessage({
                            id: 'sharedCatalog.secret.keep',
                          })
                        : intl.formatMessage({
                            id: 'sharedCatalog.secret.enter',
                          }),
                    )}
                  </Form.Item>
                );
              })}
            </Form>
          ) : (
            <PageEmpty
              description={intl.formatMessage({
                id: 'sharedCatalog.editor.noWritableFields',
              })}
            />
          )
        ) : null}
      </Modal>
    </PageContainer>
  );
}

export default function SharedCatalogResourceRoutePage() {
  const location = useLocation();
  const routeKey = findSharedCatalogResourceByPath(location.pathname)?.path ?? location.pathname;
  return <SharedCatalogResourcePage key={routeKey} />;
}
