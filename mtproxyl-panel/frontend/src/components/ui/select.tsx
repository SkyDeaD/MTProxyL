import React from 'react'
import { cn } from '@/lib/utils'

export interface SelectProps extends React.SelectHTMLAttributes<HTMLSelectElement> {}

const Select = React.forwardRef<HTMLSelectElement, SelectProps>(
  ({ className, children, ...props }, ref) => {
    return (
      <select
        ref={ref}
        style={{ borderColor: 'var(--line)' }}
        className={cn(
          'flex h-[34px] w-full rounded-[10px] border bg-surface-hover pl-[11px] pr-3 text-[12.5px] text-text-primary',
          'transition-[border-color,box-shadow] duration-150',
          'focus:outline-none focus:border-accent focus:shadow-[0_0_0_3px_var(--acc-soft)]',
          'disabled:cursor-not-allowed disabled:opacity-50',
          className,
        )}
        {...props}
      >
        {children}
      </select>
    )
  },
)
Select.displayName = 'Select'

export { Select }
