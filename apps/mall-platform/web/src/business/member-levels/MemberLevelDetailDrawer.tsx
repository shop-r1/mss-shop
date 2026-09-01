import {
  getRequestStatus,
  PageError,
  PageForbidden,
  PageLoading,
} from '@mss-boot-io/admin-web/runtime';
import { useIntl } from '@umijs/max';
import { Alert, Descriptions, Drawer, Result, Space, Typography } from 'antd';
import { localizeMemberLevelError } from './errors';
import { formatMemberLevelDate, MemberLevelDefaultTag, MemberLevelStatusTag } from './presentation';
import { useMemberLevel } from './query';

interface MemberLevelDetailDrawerProps {
  id?: string;
  canRead: boolean;
  onClose: () => void;
}

export default function MemberLevelDetailDrawer({
  id,
  canRead,
  onClose,
}: MemberLevelDetailDrawerProps) {
  const intl = useIntl();
  const detail = useMemberLevel(id, canRead);
  const formatError = (error: unknown) =>
    localizeMemberLevelError(
      error,
      (messageID, parameters) => {
        const translated = intl.formatMessage({ id: messageID }, parameters);
        return translated === messageID ? undefined : translated;
      },
      intl.formatMessage({ id: 'memberLevels.states.requestFailed' }),
    );

  return (
    <Drawer
      destroyOnHidden
      onClose={onClose}
      open={Boolean(id)}
      size="large"
      title={intl.formatMessage({ id: 'memberLevels.actions.view' })}
    >
      {detail.isPending && !detail.data ? (
        <PageLoading rows={8} />
      ) : getRequestStatus(detail.error) === 403 ? (
        <PageForbidden message={intl.formatMessage({ id: 'memberLevels.states.forbidden' })} />
      ) : getRequestStatus(detail.error) === 404 ? (
        <Result
          status="404"
          subTitle={intl.formatMessage({ id: 'memberLevels.states.notFound' })}
        />
      ) : detail.isError && !detail.data ? (
        <PageError
          message={formatError(detail.error)}
          onRetry={() => void detail.refetch()}
          retryLabel={intl.formatMessage({ id: 'memberLevels.actions.retry' })}
          title={intl.formatMessage({ id: 'memberLevels.states.error' })}
        />
      ) : detail.data ? (
        <Space orientation="vertical" size="middle" style={{ width: '100%' }}>
          {detail.isError ? (
            <Alert
              description={formatError(detail.error)}
              showIcon
              title={intl.formatMessage({
                id: 'memberLevels.states.refreshFailed',
              })}
              type="warning"
            />
          ) : null}
          <Descriptions
            bordered
            column={1}
            size="small"
            items={[
              {
                key: 'id',
                label: intl.formatMessage({ id: 'memberLevels.fields.id' }),
                children: (
                  <Typography.Text code copyable>
                    {detail.data.id}
                  </Typography.Text>
                ),
              },
              {
                key: 'name',
                label: intl.formatMessage({ id: 'memberLevels.fields.name' }),
                children: detail.data.name,
              },
              {
                key: 'discountPercent',
                label: intl.formatMessage({
                  id: 'memberLevels.fields.discountPercent',
                }),
                children: `${detail.data.discountPercent}%`,
              },
              {
                key: 'status',
                label: intl.formatMessage({ id: 'memberLevels.fields.status' }),
                children: <MemberLevelStatusTag status={detail.data.status} />,
              },
              {
                key: 'isDefault',
                label: intl.formatMessage({
                  id: 'memberLevels.fields.isDefault',
                }),
                children: <MemberLevelDefaultTag isDefault={detail.data.isDefault} />,
              },
              {
                key: 'createdAt',
                label: intl.formatMessage({
                  id: 'memberLevels.fields.createdAt',
                }),
                children: formatMemberLevelDate(detail.data.createdAt, intl.locale),
              },
              {
                key: 'updatedAt',
                label: intl.formatMessage({
                  id: 'memberLevels.fields.updatedAt',
                }),
                children: formatMemberLevelDate(detail.data.updatedAt, intl.locale),
              },
              {
                key: 'revision',
                label: intl.formatMessage({
                  id: 'memberLevels.fields.revision',
                }),
                children: (
                  <Typography.Text code copyable>
                    {detail.data.revision}
                  </Typography.Text>
                ),
              },
            ]}
          />
        </Space>
      ) : null}
    </Drawer>
  );
}
