import { cn } from '@/lib/cn'

/** 骨架屏：首屏加载时占位，避免布局跳动（Apple 风格的"内容先占位"） */
export function Skeleton({ className }: { className?: string }) {
  return <div className={cn('skeleton rounded-tile', className)} aria-hidden />
}


/** 列表行骨架（事件流首帧） */
export function RowSkeleton({ rows = 5 }: { rows?: number }) {
  return (
    <ul className="divide-y divide-hairline">
      {Array.from({ length: rows }).map((_, i) => (
        <li key={i} className="flex items-center gap-3 py-3">
          <Skeleton className="h-5 w-16 rounded-pill" />
          <Skeleton className="h-3 w-24" />
          <Skeleton className="ml-auto h-3 w-12" />
        </li>
      ))}
    </ul>
  )
}

/** 整页骨架（懒加载路由切换时的 Suspense 回退） */
export function PageSkeleton() {
  return (
    <div className="py-2">
      <Skeleton className="h-8 w-40" />
      <Skeleton className="mt-2 h-3.5 w-64" />
      <Skeleton className="mt-7 h-44 rounded-card" />
      <div className="mt-5 grid gap-4 lg:grid-cols-2">
        <Skeleton className="h-56 rounded-card" />
        <Skeleton className="h-56 rounded-card" />
      </div>
    </div>
  )
}
