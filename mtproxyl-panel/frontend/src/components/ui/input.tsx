import React from 'react'
import { cn } from '@/lib/utils'

export interface InputProps extends React.InputHTMLAttributes<HTMLInputElement> {}

const Input = React.forwardRef<HTMLInputElement, InputProps>(
  ({ className, type, ...props }, ref) => {
    return (
      <input
        type={type}
        ref={ref}
        style={{ borderColor: 'var(--line)' }}
        className={cn(
          'flex h-[34px] w-full rounded-md border bg-surface-hover px-[11px] text-sm text-text-primary',
          'transition-[border-color,box-shadow] duration-150',
          'placeholder:text-text-secondary',
          'focus:outline-none focus:border-accent focus:shadow-[0_0_0_3px_var(--acc-soft)]',
          'disabled:cursor-not-allowed disabled:opacity-50',
          className,
        )}
        {...props}
      />
    )
  },
)
Input.displayName = 'Input'

export { Input }
