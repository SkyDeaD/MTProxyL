import React from 'react'
import { cn } from '@/lib/utils'

/*
  Бейдж по системе «Азур»: мягкая подложка того же цвета, что и текст, без
  рамки. Рамка вокруг маленького цветного пятна добавляет третий контур и
  делает строку рябой.
*/
const variantStyles = {
  default: 'bg-accent/15 text-accent',
  success: 'bg-success/15 text-success',
  warning: 'bg-warning/15 text-warning',
  danger: 'bg-danger/15 text-danger',
  outline: 'bg-surface-hover text-text-secondary',
}

export interface BadgeProps extends React.HTMLAttributes<HTMLSpanElement> {
  variant?: keyof typeof variantStyles
}

const Badge = React.forwardRef<HTMLSpanElement, BadgeProps>(
  ({ className, variant = 'default', ...props }, ref) => {
    return (
      <span
        ref={ref}
        className={cn(
          'inline-flex items-center gap-[5px] h-[21px] rounded-sm px-[7px] text-2xs font-[640] whitespace-nowrap transition-colors',
          variantStyles[variant],
          className,
        )}
        {...props}
      />
    )
  },
)
Badge.displayName = 'Badge'

export { Badge }
