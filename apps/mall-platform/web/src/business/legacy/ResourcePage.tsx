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
import { legacyResourceClient } from './api';
import { findLegacyResourceByPath, type LegacyResourceEntry } from './catalog';
import { localizeLegacyError } from './errors';
import { localizeLegacyFieldLabel } from './labels';
import {
  isJSONColumn,
  isValidJSONText,
  legacyRecordID,
  toLegacyFormValues,
  toLegacyMutationPayload,
  visibleLegacyColumns,
  writableLegacyColumns,
} from './record';
import type { LegacyRecord, LegacyResourceColumn, LegacyResourceListResponse } from './types';

interface RequestFailure {
  message: string;
  status?: number;
}

interface DetailState {
  open: boolean;
  loading: boolean;
  id?: string;
  record?: LegacyRecord;
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

const initialDetailState: DetailState = { open: false, loading: false };
const initialEditorState: EditorState = {
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
    <Typography.Text style={{ maxWidth: 320 }} ellipsis={{ tooltip: text }}>
      {text}
    </Typography.Text>
  );
}

function renderDetailValue(value: unknown, booleanLabels: { yes: string; no: string }): ReactNode {
  if (value !== null && typeof value === 'object') {
    return (
      <Typography.Paragraph copyable={{ text: JSON.stringify(value) }} style={{ marginBottom: 0 }}>
        <pre
          style={{
            margin: 0,
            maxHeight: 320,
            overflow: 'auto',
            whiteSpace: 'pre-wrap',
          }}
        >
          {JSON.stringify(value, null, 2)}
        </pre>
      </Typography.Paragraph>
    );
  }
  return renderValue(value, booleanLabels);
}

function fieldControl(column: LegacyResourceColumn, secretPlaceholder: string) {
  const type = column.type.toLowerCase();
  if (column.secret) {
    return <Input.Password autoComplete="new-password" placeholder={secretPlaceholder} />;
  }
  if (type === 'boolean' || type === 'bool') {
    return <Switch />;
  }
  if (
    type === 'integer' ||
    type === 'int' ||
    type === 'number' ||
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
  if (type === 'date') {
    return <Input type="date" />;
  }
  if (type === 'datetime' || type === 'timestamp') {
    return <Input type="datetime-local" />;
  }
  return <Input />;
}

function LegacyResourcePage() {
  const intl = useIntl();
  const location = useLocation();
  const { message } = App.useApp();
  const { initialState } = useModel('@@initialState') as {
    initialState?: InitialState;
  };
  const user = initialState?.currentUser;
  const entry = findLegacyResourceByPath(location.pathname);
  const [form] = Form.useForm<LegacyRecord>();
  const formatError = useCallback(
    (error: unknown) =>
      localizeLegacyError(error, (id, params) => intl.formatMessage({ id }, params)),
    [intl],
  );

  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [searchInput, setSearchInput] = useState('');
  const [query, setQuery] = useState('');
  const [list, setList] = useState<LegacyResourceListResponse>();
  const [loading, setLoading] = useState(false);
  const [listError, setListError] = useState<RequestFailure>();
  const [detail, setDetail] = useState<DetailState>(initialDetailState);
  const [editor, setEditor] = useState<EditorState>(initialEditorState);
  const [deletingID, setDeletingID] = useState<string>();

  const canRoute = Boolean(entry && hasPermission(user, entry.routePermission));
  const canList = Boolean(entry && canRoute && hasPermission(user, entry.listPermission));

  const loadList = useCallback(async () => {
    if (!entry || !canList) return;
    setLoading(true);
    setListError(undefined);
    try {
      const result = await legacyResourceClient.list(entry, {
        page,
        pageSize,
        q: query || undefined,
      });
      setList(result);
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
      yes: intl.formatMessage({ id: 'legacy.value.yes' }),
      no: intl.formatMessage({ id: 'legacy.value.no' }),
    }),
    [intl],
  );

  const labelFor = useCallback(
    (column: LegacyResourceColumn) =>
      localizeLegacyFieldLabel(column, (messageDescriptor) =>
        intl.formatMessage(messageDescriptor),
      ),
    [intl],
  );

  const canRead = Boolean(
    entry &&
      descriptor?.capabilities.detail &&
      canRoute &&
      hasPermission(user, entry.readPermission),
  );
  const canCreate = Boolean(
    entry &&
      descriptor?.capabilities.create &&
      canRoute &&
      hasPermission(user, entry.createPermission),
  );
  const canUpdate = Boolean(
    entry &&
      descriptor?.capabilities.detail &&
      descriptor?.capabilities.update &&
      canRoute &&
      hasPermission(user, entry.updatePermission),
  );
  const canDelete = Boolean(
    entry &&
      descriptor?.capabilities.delete &&
      canRoute &&
      hasPermission(user, entry.deletePermission),
  );

  const openDetail = useCallback(
    async (resource: LegacyResourceEntry, id: string) => {
      setDetail({ open: true, loading: true, id });
      try {
        const record = await legacyResourceClient.detail(resource, id);
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
    async (resource: LegacyResourceEntry, id: string) => {
      form.resetFields();
      setEditor({ open: true, loading: true, saving: false, mode: 'edit', id });
      try {
        const record = await legacyResourceClient.detail(resource, id);
        form.setFieldsValue(toLegacyFormValues(descriptor?.columns ?? [], record));
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

  const deleteRecord = useCallback(
    async (resource: LegacyResourceEntry, id: string) => {
      setDeletingID(id);
      try {
        await legacyResourceClient.remove(resource, id);
        message.success(intl.formatMessage({ id: 'legacy.feedback.deleted' }));
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
    async (values: LegacyRecord) => {
      if (!entry || !descriptor) return;
      setEditor((current) => ({ ...current, saving: true, error: undefined }));
      try {
        const payload = toLegacyMutationPayload(descriptor.columns, values);
        if (editor.mode === 'create') {
          await legacyResourceClient.create(entry, payload);
        } else if (editor.id) {
          await legacyResourceClient.update(entry, editor.id, payload);
        }
        message.success(
          intl.formatMessage({
            id: editor.mode === 'create' ? 'legacy.feedback.created' : 'legacy.feedback.updated',
          }),
        );
        setEditor(initialEditorState);
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

  const tableColumns = useMemo<TableColumnsType<LegacyRecord>>(() => {
    if (!entry || !descriptor) return [];
    const columns: TableColumnsType<LegacyRecord> = visibleLegacyColumns(descriptor.columns).map(
      (column) => ({
        title: labelFor(column),
        dataIndex: column.name,
        key: column.name,
        ellipsis: true,
        render: (value: unknown) => renderValue(value, booleanLabels),
      }),
    );
    if (canRead || canUpdate || canDelete)
      columns.push({
        title: intl.formatMessage({ id: 'legacy.table.actions' }),
        key: '__actions',
        fixed: 'right',
        width: 190,
        render: (_value: unknown, record: LegacyRecord) => {
          const id = legacyRecordID(record);
          return (
            <Space size="small">
              {canRead ? (
                <Button
                  aria-label={intl.formatMessage({ id: 'legacy.action.view' })}
                  disabled={!id}
                  icon={<EyeOutlined />}
                  onClick={() => id && void openDetail(entry, id)}
                  size="small"
                  type="link"
                />
              ) : null}
              {canUpdate ? (
                <Button
                  aria-label={intl.formatMessage({ id: 'legacy.action.edit' })}
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
                    id: 'legacy.action.cancel',
                  })}
                  description={intl.formatMessage({
                    id: 'legacy.delete.description',
                  })}
                  disabled={!id}
                  okButtonProps={{
                    danger: true,
                    loading: Boolean(id && deletingID === id),
                  }}
                  okText={intl.formatMessage({ id: 'legacy.action.delete' })}
                  onConfirm={() => id && deleteRecord(entry, id)}
                  title={intl.formatMessage({ id: 'legacy.delete.title' })}
                >
                  <Button
                    aria-label={intl.formatMessage({
                      id: 'legacy.action.delete',
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
    return columns;
  }, [
    booleanLabels,
    canDelete,
    canRead,
    canUpdate,
    deleteRecord,
    deletingID,
    descriptor,
    entry,
    intl,
    labelFor,
    openDetail,
    openEdit,
  ]);

  if (!entry) {
    return (
      <Result
        status="404"
        title="404"
        subTitle={intl.formatMessage({ id: 'legacy.error.unknownResource' })}
      />
    );
  }

  if (!canRoute || !canList) {
    return <PageForbidden message={intl.formatMessage({ id: 'legacy.error.forbidden' })} />;
  }

  const catalogTitle = intl.formatMessage({ id: entry.titleKey });
  const title = intl.formatMessage({
    id: descriptor?.titleKey ?? entry.titleKey,
    defaultMessage: catalogTitle,
  });
  const description = intl.formatMessage({ id: entry.domainTitleKey });

  let content: ReactNode;
  if (listError?.status === 403) {
    content = <PageForbidden message={listError.message} />;
  } else if (listError) {
    const unavailable = listError.status === 503;
    content = (
      <PageError
        message={listError.message}
        onRetry={() => void loadList()}
        title={
          unavailable
            ? intl.formatMessage({ id: 'legacy.error.unavailableTitle' })
            : intl.formatMessage({ id: 'legacy.error.loadTitle' })
        }
      />
    );
  } else if (!list && loading) {
    content = <PageLoading rows={7} />;
  } else if (list && list.data.length === 0) {
    content = <PageEmpty description={intl.formatMessage({ id: 'legacy.empty' })} />;
  } else {
    content = (
      <>
        <Table<LegacyRecord>
          columns={tableColumns}
          dataSource={list?.data ?? []}
          loading={loading}
          pagination={false}
          rowKey={(record) =>
            legacyRecordID(record) ?? `row-${Math.max(0, list?.data.indexOf(record) ?? 0)}`
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
            showTotal={(total) => intl.formatMessage({ id: 'legacy.pagination.total' }, { total })}
            total={list?.total ?? 0}
          />
        </div>
      </>
    );
  }

  const detailColumns = descriptor ? visibleLegacyColumns(descriptor.columns) : [];
  const formColumns = descriptor ? writableLegacyColumns(descriptor.columns) : [];

  return (
    <PageContainer content={description} title={title}>
      <Space orientation="vertical" size="large" style={{ width: '100%' }}>
        <Space wrap style={{ display: 'flex', justifyContent: 'space-between' }}>
          <Input.Search
            allowClear
            enterButton={intl.formatMessage({ id: 'legacy.search.submit' })}
            onChange={(event) => setSearchInput(event.target.value)}
            onSearch={(value) => {
              setPage(1);
              setQuery(value.trim());
            }}
            placeholder={intl.formatMessage({
              id: 'legacy.search.placeholder',
            })}
            style={{ maxWidth: 480 }}
            value={searchInput}
          />
          <Space>
            <Button icon={<ReloadOutlined />} loading={loading} onClick={() => void loadList()}>
              {intl.formatMessage({ id: 'legacy.action.refresh' })}
            </Button>
            {canCreate ? (
              <Button icon={<PlusOutlined />} onClick={openCreate} type="primary">
                {intl.formatMessage({ id: 'legacy.action.create' })}
              </Button>
            ) : null}
          </Space>
        </Space>
        {content}
      </Space>

      <Drawer
        destroyOnHidden
        onClose={() => setDetail(initialDetailState)}
        open={detail.open}
        title={intl.formatMessage({ id: 'legacy.detail.title' }, { id: detail.id ?? '' })}
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
        cancelText={intl.formatMessage({ id: 'legacy.action.cancel' })}
        confirmLoading={editor.saving}
        destroyOnHidden
        mask={{ closable: false }}
        okButtonProps={{ disabled: editor.loading || formColumns.length === 0 }}
        okText={intl.formatMessage({ id: 'legacy.action.save' })}
        onCancel={() => {
          setEditor(initialEditorState);
          form.resetFields();
        }}
        onOk={() => form.submit()}
        open={editor.open}
        title={intl.formatMessage(
          {
            id: editor.mode === 'create' ? 'legacy.editor.createTitle' : 'legacy.editor.editTitle',
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
            <Form<LegacyRecord>
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
                          { id: 'legacy.validation.required' },
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
                                          id: 'legacy.validation.json',
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
                        ? intl.formatMessage({ id: 'legacy.secret.keep' })
                        : intl.formatMessage({ id: 'legacy.secret.enter' }),
                    )}
                  </Form.Item>
                );
              })}
            </Form>
          ) : (
            <PageEmpty
              description={intl.formatMessage({
                id: 'legacy.editor.noWritableFields',
              })}
            />
          )
        ) : null}
      </Modal>
    </PageContainer>
  );
}

export default function LegacyResourceRoutePage() {
  const location = useLocation();
  const routeKey = findLegacyResourceByPath(location.pathname)?.path ?? location.pathname;
  return <LegacyResourcePage key={routeKey} />;
}
