import { Navigate, useParams } from 'react-router'

/** 旧设备面板链接统一到设备详情；不再用业务名称包装通用设备观测。 */
export default function LegacyDevicePanelRedirect() {
  const { edgeId, deviceId } = useParams()
  const target = edgeId && deviceId
    ? `/devices/${encodeURIComponent(edgeId)}/${encodeURIComponent(deviceId)}?tab=controls`
    : '/devices'
  return <Navigate to={target} replace />
}
