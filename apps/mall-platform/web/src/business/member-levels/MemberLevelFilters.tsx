import { SearchOutlined } from '@ant-design/icons';
import { useIntl } from '@umijs/max';
import { Button, Col, Form, type FormInstance, Input, Row, Select, Space } from 'antd';
import { memberLevelNameMaxBytes, utf8ByteLength } from './contract';
import type { MemberLevelFilterValues } from './types';

interface MemberLevelFiltersProps {
  form: FormInstance<MemberLevelFilterValues>;
  initialValues: MemberLevelFilterValues;
  onReset: () => void;
  onSubmit: (values: MemberLevelFilterValues) => void;
}

export default function MemberLevelFilters({
  form,
  initialValues,
  onReset,
  onSubmit,
}: MemberLevelFiltersProps) {
  const intl = useIntl();
  return (
    <Form<MemberLevelFilterValues>
      form={form}
      initialValues={initialValues}
      layout="vertical"
      name="member-level-filters"
      onFinish={onSubmit}
    >
      <Row align="bottom" gutter={12}>
        <Col xs={24} md={10} lg={8}>
          <Form.Item
            label={intl.formatMessage({ id: 'memberLevels.fields.name' })}
            name="q"
            rules={[
              {
                validator: (_rule: unknown, value: unknown) =>
                  typeof value !== 'string' ||
                  utf8ByteLength(value.trim()) <= memberLevelNameMaxBytes
                    ? Promise.resolve()
                    : Promise.reject(
                        new Error(
                          intl.formatMessage(
                            { id: 'memberLevels.validation.searchMaxBytes' },
                            { max: memberLevelNameMaxBytes },
                          ),
                        ),
                      ),
              },
            ]}
          >
            <Input
              allowClear
              autoComplete="off"
              maxLength={memberLevelNameMaxBytes}
              placeholder={intl.formatMessage({
                id: 'memberLevels.fields.nameSearch',
              })}
            />
          </Form.Item>
        </Col>
        <Col xs={12} md={6} lg={5}>
          <Form.Item label={intl.formatMessage({ id: 'memberLevels.fields.status' })} name="status">
            <Select
              options={[
                {
                  value: 'all',
                  label: intl.formatMessage({ id: 'memberLevels.values.all' }),
                },
                {
                  value: 'enabled',
                  label: intl.formatMessage({
                    id: 'memberLevels.values.status.enabled',
                  }),
                },
                {
                  value: 'disabled',
                  label: intl.formatMessage({
                    id: 'memberLevels.values.status.disabled',
                  }),
                },
              ]}
              virtual={false}
            />
          </Form.Item>
        </Col>
        <Col xs={12} md={6} lg={5}>
          <Form.Item
            label={intl.formatMessage({ id: 'memberLevels.fields.isDefault' })}
            name="isDefault"
          >
            <Select
              options={[
                {
                  value: 'all',
                  label: intl.formatMessage({ id: 'memberLevels.values.all' }),
                },
                {
                  value: 'true',
                  label: intl.formatMessage({
                    id: 'memberLevels.values.default.yes',
                  }),
                },
                {
                  value: 'false',
                  label: intl.formatMessage({
                    id: 'memberLevels.values.default.no',
                  }),
                },
              ]}
              virtual={false}
            />
          </Form.Item>
        </Col>
        <Col flex="none">
          <Form.Item>
            <Space>
              <Button htmlType="submit" icon={<SearchOutlined />} type="primary">
                {intl.formatMessage({ id: 'memberLevels.actions.search' })}
              </Button>
              <Button onClick={onReset}>
                {intl.formatMessage({ id: 'memberLevels.actions.reset' })}
              </Button>
            </Space>
          </Form.Item>
        </Col>
      </Row>
    </Form>
  );
}
