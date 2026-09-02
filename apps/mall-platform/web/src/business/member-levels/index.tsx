import { PlusOutlined, ReloadOutlined } from '@ant-design/icons';
import {
  getRequestStatus,
  type InitialState,
  PageContainer,
  PageError,
  PageForbidden,
  PageLoading,
} from '@mss-boot-io/admin-web/runtime';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useIntl, useModel } from '@umijs/max';
import { Alert, App, Button, Col, Form, Row, Space, Typography } from 'antd';
import { useCallback, useMemo, useState } from 'react';
import { memberLevelsAPI } from './api';
import {
  buildMemberLevelQuery,
  createMemberLevelInput,
  getMemberLevelCapabilities,
  intersectMemberLevelOperations,
  updateMemberLevelInput,
} from './contract';
import { localizeMemberLevelError } from './errors';
import MemberLevelDetailDrawer from './MemberLevelDetailDrawer';
import MemberLevelEditorModal from './MemberLevelEditorModal';
import MemberLevelFilters from './MemberLevelFilters';
import MemberLevelTable from './MemberLevelTable';
import { memberLevelQueryKeys, useMemberLevelPage } from './query';
import type {
  MemberLevel,
  MemberLevelEditorValues,
  MemberLevelFilterValues,
  MemberLevelQuery,
} from './types';

const initialFilters: MemberLevelFilterValues = {
  status: 'all',
  isDefault: 'all',
};

interface SaveMemberLevelVariables {
  record?: MemberLevel;
  values: MemberLevelEditorValues;
}

function MemberLevelsPage() {
  const intl = useIntl();
  const { message } = App.useApp();
  const queryClient = useQueryClient();
  const { initialState } = useModel('@@initialState') as {
    initialState?: InitialState;
  };
  const capabilities = useMemo(
    () => getMemberLevelCapabilities(initialState?.currentUser),
    [initialState?.currentUser],
  );
  const [filterForm] = Form.useForm<MemberLevelFilterValues>();
  const [filters, setFilters] = useState<MemberLevelFilterValues>(initialFilters);
  const [params, setParams] = useState<MemberLevelQuery>(() =>
    buildMemberLevelQuery(1, 20, initialFilters),
  );
  const [detailID, setDetailID] = useState<string>();
  const [editorOpen, setEditorOpen] = useState(false);
  const [editorRecord, setEditorRecord] = useState<MemberLevel>();
  const [editorSession, setEditorSession] = useState(0);

  const list = useMemberLevelPage(params, capabilities.canList);
  const effectiveCapabilities = useMemo(
    () => intersectMemberLevelOperations(capabilities, list.data?.operations),
    [capabilities, list.data?.operations],
  );
  const formatError = useCallback(
    (error: unknown) =>
      localizeMemberLevelError(
        error,
        (messageID, parameters) => {
          const translated = intl.formatMessage({ id: messageID }, parameters);
          return translated === messageID ? undefined : translated;
        },
        intl.formatMessage({ id: 'memberLevels.states.requestFailed' }),
      ),
    [intl],
  );
  const closeEditor = () => {
    setEditorOpen(false);
    setEditorRecord(undefined);
  };

  const save = useMutation({
    mutationFn: ({ record, values }: SaveMemberLevelVariables) =>
      record
        ? memberLevelsAPI.update(record.id, updateMemberLevelInput(values, record.revision))
        : memberLevelsAPI.create(createMemberLevelInput(values)),
    onSuccess: async (record, variables) => {
      queryClient.setQueryData(memberLevelQueryKeys.detail(record.id), record);
      await queryClient.invalidateQueries({
        queryKey: memberLevelQueryKeys.lists,
      });
      closeEditor();
      void message.success(
        intl.formatMessage({
          id: variables.record ? 'memberLevels.feedback.updated' : 'memberLevels.feedback.created',
        }),
      );
    },
  });

  const setDefault = useMutation({
    mutationFn: (record: MemberLevel) => memberLevelsAPI.setDefault(record.id, record.revision),
    onSuccess: async () => {
      await queryClient.invalidateQueries({
        queryKey: memberLevelQueryKeys.all,
      });
      void message.success(intl.formatMessage({ id: 'memberLevels.feedback.defaultChanged' }));
    },
  });

  const remove = useMutation({
    mutationFn: (record: MemberLevel) => memberLevelsAPI.remove(record.id, record.revision),
    onSuccess: async (_result, record) => {
      if ((list.data?.data.length ?? 0) === 1 && params.current > 1) {
        setParams((current) => ({ ...current, current: current.current - 1 }));
      }
      if (detailID === record.id) setDetailID(undefined);
      await queryClient.invalidateQueries({
        queryKey: memberLevelQueryKeys.all,
      });
      void message.success(intl.formatMessage({ id: 'memberLevels.feedback.deleted' }));
    },
  });

  const openCreate = () => {
    save.reset();
    setEditorRecord(undefined);
    setEditorSession((session) => session + 1);
    setEditorOpen(true);
  };
  const openEdit = (record: MemberLevel) => {
    save.reset();
    setEditorRecord(record);
    setEditorSession((session) => session + 1);
    setEditorOpen(true);
  };
  const clearFilters = () => {
    filterForm.resetFields();
    setFilters(initialFilters);
    setParams((current) => buildMemberLevelQuery(1, current.pageSize, initialFilters));
  };

  const title = intl.formatMessage({ id: 'memberLevels.title' });
  const description = intl.formatMessage({ id: 'memberLevels.description' });
  const hasActiveFilters = Boolean(
    params.q || params.status !== undefined || params.isDefault !== undefined,
  );
  const unknownCount = list.data?.data.filter((record) => record.status === 'unknown').length ?? 0;
  const integrity = list.data?.integrity;
  const defaultIntegrityHealthy =
    integrity !== undefined &&
    integrity.flaggedDefaultCount === 1 &&
    integrity.enabledDefaultCount === 1 &&
    integrity.invalidDefaultCount === 0;
  const hasMutationPermission =
    capabilities.canCreate ||
    capabilities.canUpdate ||
    capabilities.canSetDefault ||
    capabilities.canDelete;
  const mutationGateClosed =
    hasMutationPermission &&
    list.data !== undefined &&
    !list.data.operations.create &&
    !list.data.operations.update &&
    !list.data.operations.setDefault &&
    !list.data.operations.delete;
  const deleteGateClosed =
    capabilities.canDelete &&
    list.data !== undefined &&
    !list.data.operations.delete &&
    (list.data.operations.create || list.data.operations.update || list.data.operations.setDefault);

  if (!capabilities.canList || getRequestStatus(list.error) === 403) {
    return (
      <PageContainer content={description} title={title}>
        <PageForbidden message={intl.formatMessage({ id: 'memberLevels.states.forbidden' })} />
      </PageContainer>
    );
  }
  if (list.isPending && !list.data) {
    return (
      <PageContainer content={description} title={title}>
        <PageLoading rows={8} />
      </PageContainer>
    );
  }
  if (list.isError && !list.data) {
    return (
      <PageContainer content={description} title={title}>
        <PageError
          message={formatError(list.error)}
          onRetry={() => void list.refetch()}
          retryLabel={intl.formatMessage({ id: 'memberLevels.actions.retry' })}
          title={intl.formatMessage({ id: 'memberLevels.states.error' })}
        />
      </PageContainer>
    );
  }

  const sourceLevels = list.data?.data.filter((record) => record.status === 'enabled') ?? [];

  return (
    <PageContainer content={description} title={title}>
      <Space orientation="vertical" size="middle" style={{ width: '100%' }}>
        {list.isError && list.data ? (
          <Alert
            action={
              <Button icon={<ReloadOutlined />} onClick={() => void list.refetch()} size="small">
                {intl.formatMessage({ id: 'memberLevels.actions.retry' })}
              </Button>
            }
            description={formatError(list.error)}
            showIcon
            title={intl.formatMessage({
              id: 'memberLevels.states.refreshFailed',
            })}
            type="warning"
          />
        ) : null}
        {mutationGateClosed ? (
          <Alert
            description={intl.formatMessage({
              id: 'memberLevels.states.mutationGateClosedDescription',
            })}
            showIcon
            title={intl.formatMessage({
              id: 'memberLevels.states.mutationGateClosed',
            })}
            type="info"
          />
        ) : deleteGateClosed ? (
          <Alert
            description={intl.formatMessage({
              id: 'memberLevels.states.deleteGateClosedDescription',
            })}
            showIcon
            title={intl.formatMessage({
              id: 'memberLevels.states.deleteGateClosed',
            })}
            type="info"
          />
        ) : null}
        {integrity !== undefined && !defaultIntegrityHealthy ? (
          <Alert
            description={intl.formatMessage(
              {
                id:
                  integrity.invalidDefaultCount > 0
                    ? 'memberLevels.states.defaultInvalidDescription'
                    : integrity.enabledDefaultCount === 0
                      ? 'memberLevels.states.defaultMissingDescription'
                      : 'memberLevels.states.defaultDuplicateDescription',
              },
              {
                flaggedCount: integrity.flaggedDefaultCount,
                enabledCount: integrity.enabledDefaultCount,
                invalidCount: integrity.invalidDefaultCount,
              },
            )}
            showIcon
            title={intl.formatMessage({
              id:
                integrity.invalidDefaultCount > 0
                  ? 'memberLevels.states.defaultInvalid'
                  : integrity.enabledDefaultCount === 0
                    ? 'memberLevels.states.defaultMissing'
                    : 'memberLevels.states.defaultDuplicate',
            })}
            type="warning"
          />
        ) : null}
        {unknownCount > 0 ? (
          <Alert
            description={intl.formatMessage(
              { id: 'memberLevels.states.unknownRowsDescription' },
              { count: unknownCount },
            )}
            showIcon
            title={intl.formatMessage({
              id: 'memberLevels.states.unknownRows',
            })}
            type="warning"
          />
        ) : null}
        {setDefault.isError ? (
          <Alert
            closable
            description={formatError(setDefault.error)}
            onClose={() => setDefault.reset()}
            showIcon
            title={intl.formatMessage({
              id: 'memberLevels.states.setDefaultError',
            })}
            type={
              [409, 412].includes(getRequestStatus(setDefault.error) ?? 0) ? 'warning' : 'error'
            }
          />
        ) : null}
        {remove.isError ? (
          <Alert
            closable
            description={formatError(remove.error)}
            onClose={() => remove.reset()}
            showIcon
            title={intl.formatMessage({
              id: 'memberLevels.states.deleteError',
            })}
            type={[409, 412].includes(getRequestStatus(remove.error) ?? 0) ? 'warning' : 'error'}
          />
        ) : null}

        <MemberLevelFilters
          form={filterForm}
          initialValues={initialFilters}
          onReset={clearFilters}
          onSubmit={(values) => {
            setFilters(values);
            setParams(buildMemberLevelQuery(1, params.pageSize, values));
          }}
        />
        <Row align="middle" justify="space-between" gutter={[12, 12]}>
          <Col>
            <Typography.Title level={4} style={{ margin: 0 }}>
              {intl.formatMessage({ id: 'memberLevels.list.title' })}
            </Typography.Title>
          </Col>
          <Col>
            <Space wrap>
              <Button
                icon={<ReloadOutlined />}
                loading={list.isFetching}
                onClick={() => void list.refetch()}
              >
                {intl.formatMessage({ id: 'memberLevels.actions.refresh' })}
              </Button>
              {effectiveCapabilities.canCreate ? (
                <Button icon={<PlusOutlined />} onClick={openCreate} type="primary">
                  {intl.formatMessage({ id: 'memberLevels.actions.create' })}
                </Button>
              ) : null}
            </Space>
          </Col>
        </Row>
        <MemberLevelTable
          capabilities={effectiveCapabilities}
          current={params.current}
          data={list.data?.data ?? []}
          defaultIntegrityHealthy={defaultIntegrityHealthy}
          deletingID={remove.variables?.id}
          deletePending={remove.isPending}
          filtered={hasActiveFilters}
          loading={list.isFetching}
          onClearFilters={clearFilters}
          onCreate={openCreate}
          onDelete={(record) => remove.mutate(record)}
          onEdit={openEdit}
          onPageChange={(current, pageSize) =>
            setParams(buildMemberLevelQuery(current, pageSize, filters))
          }
          onSetDefault={(record) => setDefault.mutate(record)}
          onView={(record) => setDetailID(record.id)}
          pageSize={params.pageSize}
          settingDefaultID={setDefault.variables?.id}
          setDefaultPending={setDefault.isPending}
          total={list.data?.total ?? 0}
        />
      </Space>

      <MemberLevelEditorModal
        error={save.isError ? formatError(save.error) : undefined}
        errorIsConflict={save.isError && [409, 412].includes(getRequestStatus(save.error) ?? 0)}
        onCancel={() => {
          if (save.isPending) return;
          closeEditor();
          save.reset();
        }}
        onSubmit={(values) => save.mutate({ record: editorRecord, values })}
        open={editorOpen}
        record={editorRecord}
        saving={save.isPending}
        session={editorSession}
        sourceLevels={sourceLevels}
      />
      <MemberLevelDetailDrawer
        canRead={capabilities.canRead}
        id={detailID}
        onClose={() => setDetailID(undefined)}
      />
    </PageContainer>
  );
}

export default MemberLevelsPage;
