import {
  DeleteOutlined,
  EditOutlined,
  EyeOutlined,
  PlusOutlined,
  StarOutlined,
} from '@ant-design/icons';
import { PageEmpty } from '@mss-boot-io/admin-web/runtime';
import { useIntl } from '@umijs/max';
import {
  Button,
  Popconfirm,
  Space,
  Table,
  type TableColumnsType,
  type TableProps,
  Tooltip,
} from 'antd';
import { memberLevelPageSizes } from './contract';
import { formatMemberLevelDate, MemberLevelDefaultTag, MemberLevelStatusTag } from './presentation';
import type { MemberLevel, MemberLevelCapabilities } from './types';

interface MemberLevelTableProps {
  capabilities: MemberLevelCapabilities;
  current: number;
  data: MemberLevel[];
  defaultIntegrityHealthy?: boolean;
  deletingID?: string;
  deletePending: boolean;
  filtered: boolean;
  loading: boolean;
  pageSize: number;
  settingDefaultID?: string;
  setDefaultPending: boolean;
  total: number;
  onClearFilters: () => void;
  onCreate: () => void;
  onDelete: (record: MemberLevel) => void;
  onEdit: (record: MemberLevel) => void;
  onPageChange: (current: number, pageSize: number) => void;
  onSetDefault: (record: MemberLevel) => void;
  onView: (record: MemberLevel) => void;
}

export default function MemberLevelTable({
  capabilities,
  current,
  data,
  defaultIntegrityHealthy,
  deletingID,
  deletePending,
  filtered,
  loading,
  pageSize,
  settingDefaultID,
  setDefaultPending,
  total,
  onClearFilters,
  onCreate,
  onDelete,
  onEdit,
  onPageChange,
  onSetDefault,
  onView,
}: MemberLevelTableProps) {
  const intl = useIntl();
  const columns: TableColumnsType<MemberLevel> = [
    {
      title: intl.formatMessage({ id: 'memberLevels.fields.name' }),
      dataIndex: 'name',
      ellipsis: true,
    },
    {
      title: intl.formatMessage({ id: 'memberLevels.fields.discountPercent' }),
      dataIndex: 'discountPercent',
      align: 'right',
      width: 150,
      render: (value: string) => `${value}%`,
    },
    {
      title: intl.formatMessage({ id: 'memberLevels.fields.status' }),
      dataIndex: 'status',
      width: 120,
      render: (_value, record) => <MemberLevelStatusTag status={record.status} />,
    },
    {
      title: intl.formatMessage({ id: 'memberLevels.fields.isDefault' }),
      dataIndex: 'isDefault',
      width: 120,
      render: (_value, record) => <MemberLevelDefaultTag isDefault={record.isDefault} />,
    },
    {
      title: intl.formatMessage({ id: 'memberLevels.fields.updatedAt' }),
      dataIndex: 'updatedAt',
      responsive: ['lg'],
      width: 200,
      render: (value: string) => formatMemberLevelDate(value, intl.locale),
    },
    {
      title: intl.formatMessage({ id: 'memberLevels.actions.label' }),
      fixed: 'right',
      key: 'actions',
      width: 390,
      render: (_value, record) => {
        const defaultBlocked = record.status !== 'enabled';
        const deleteBlocked = record.isDefault;
        const canOfferSetDefault =
          capabilities.canSetDefault && (!record.isDefault || defaultIntegrityHealthy === false);
        const defaultLoading = setDefaultPending && settingDefaultID === record.id;
        const deleteLoading = deletePending && deletingID === record.id;
        const defaultButton = (
          <Button
            disabled={defaultBlocked || setDefaultPending}
            icon={<StarOutlined />}
            loading={defaultLoading}
            size="small"
            type="link"
          >
            {intl.formatMessage({ id: 'memberLevels.actions.setDefault' })}
          </Button>
        );
        const deleteButton = (
          <Button
            danger
            disabled={deleteBlocked || deletePending}
            icon={<DeleteOutlined />}
            loading={deleteLoading}
            size="small"
            type="link"
          >
            {intl.formatMessage({ id: 'memberLevels.actions.delete' })}
          </Button>
        );
        return (
          <Space size="small" wrap>
            {capabilities.canRead ? (
              <Button
                icon={<EyeOutlined />}
                onClick={() => onView(record)}
                size="small"
                type="link"
              >
                {intl.formatMessage({ id: 'memberLevels.actions.view' })}
              </Button>
            ) : null}
            {capabilities.canUpdate ? (
              <Button
                icon={<EditOutlined />}
                onClick={() => onEdit(record)}
                size="small"
                type="link"
              >
                {intl.formatMessage({ id: 'memberLevels.actions.edit' })}
              </Button>
            ) : null}
            {canOfferSetDefault ? (
              defaultBlocked ? (
                <Tooltip
                  title={intl.formatMessage({
                    id: 'memberLevels.actions.setDefaultDisabledHelp',
                  })}
                >
                  <span>{defaultButton}</span>
                </Tooltip>
              ) : (
                <Popconfirm
                  cancelText={intl.formatMessage({
                    id: 'memberLevels.actions.cancel',
                  })}
                  description={intl.formatMessage(
                    { id: 'memberLevels.confirm.setDefaultDescription' },
                    { name: record.name },
                  )}
                  okButtonProps={{ loading: defaultLoading }}
                  okText={intl.formatMessage({
                    id: 'memberLevels.actions.setDefault',
                  })}
                  onConfirm={() => onSetDefault(record)}
                  title={intl.formatMessage({
                    id: 'memberLevels.confirm.setDefaultTitle',
                  })}
                >
                  {defaultButton}
                </Popconfirm>
              )
            ) : null}
            {capabilities.canDelete ? (
              deleteBlocked ? (
                <Tooltip
                  title={intl.formatMessage({
                    id: 'memberLevels.actions.deleteDefaultHelp',
                  })}
                >
                  <span>{deleteButton}</span>
                </Tooltip>
              ) : (
                <Popconfirm
                  cancelText={intl.formatMessage({
                    id: 'memberLevels.actions.cancel',
                  })}
                  description={intl.formatMessage(
                    { id: 'memberLevels.confirm.deleteDescription' },
                    { name: record.name },
                  )}
                  okButtonProps={{ danger: true, loading: deleteLoading }}
                  okText={intl.formatMessage({
                    id: 'memberLevels.actions.delete',
                  })}
                  onConfirm={() => onDelete(record)}
                  title={intl.formatMessage({
                    id: 'memberLevels.confirm.deleteTitle',
                  })}
                >
                  {deleteButton}
                </Popconfirm>
              )
            ) : null}
          </Space>
        );
      },
    },
  ];
  const emptyText = (
    <Space orientation="vertical" size="small" style={{ padding: 16 }}>
      <PageEmpty
        description={intl.formatMessage({
          id: filtered ? 'memberLevels.states.filteredEmpty' : 'memberLevels.states.empty',
        })}
      />
      {filtered ? (
        <Button onClick={onClearFilters}>
          {intl.formatMessage({ id: 'memberLevels.actions.clearFilters' })}
        </Button>
      ) : capabilities.canCreate ? (
        <Button icon={<PlusOutlined />} onClick={onCreate} type="primary">
          {intl.formatMessage({ id: 'memberLevels.actions.create' })}
        </Button>
      ) : null}
    </Space>
  );
  const handleChange: TableProps<MemberLevel>['onChange'] = (pagination) => {
    onPageChange(
      pagination.pageSize !== pageSize ? 1 : (pagination.current ?? 1),
      pagination.pageSize ?? 20,
    );
  };

  return (
    <Table<MemberLevel>
      columns={columns}
      dataSource={data}
      loading={loading}
      locale={{ emptyText }}
      onChange={handleChange}
      pagination={{
        current,
        pageSize,
        pageSizeOptions: memberLevelPageSizes,
        showSizeChanger: true,
        showTotal: (count) =>
          intl.formatMessage({ id: 'memberLevels.pagination.total' }, { total: count }),
        total,
      }}
      rowKey="id"
      scroll={{ x: 'max-content' }}
    />
  );
}
