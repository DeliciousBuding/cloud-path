import { Link } from 'react-router'
import { usePageTitle } from '@/hooks/usePageTitle'
import { Compass, ArrowLeft } from 'lucide-react'
import { EmptyState } from '@/components/ui'

export default function NotFound() {
  usePageTitle('页面不存在')

  return (
    <div className="py-10">
      <EmptyState
        icon={<Compass size={24} />}
        title="页面不存在"
        hint="地址可能有误，或该页面已被移除。"
      />
      <div className="mt-5 flex justify-center">
        <Link to="/" className="btn btn-ghost">
          <ArrowLeft size={14} /> 返回概览
        </Link>
      </div>
    </div>
  )
}
