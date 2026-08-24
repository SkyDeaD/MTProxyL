import React from 'react'
import { cn } from '@/lib/utils'

/*
  Кнопки по системе «Азур»: один primary на секцию, остальное — тихие
  варианты. Опасное действие красное не заливкой, а мягким фоном с красным
  текстом: сплошная красная кнопка в интерфейсе, где удаление рядом с
  сохранением, слишком тянет на себя палец.
*/
const variantStyles = {
  default: 'bg-accent border border-accent text-[var(--acc-fg)] font-semibold hover:brightness-110',
  outline: 'border bg-surface text-text-primary hover:bg-surface-hover hover:border-text-secondary',
  ghost: 'bg-transparent border border-transparent text-text-secondary hover:bg-surface-hover hover:text-text-primary',
  danger: 'bg-danger/15 border border-transparent text-danger hover:brightness-110',
}

const sizeStyles = {
  sm: 'h-7 px-2.5 text-xs gap-1.5',
  default: 'h-[34px] px-3.5 text-[12.5px] gap-[7px]',
  lg: 'h-10 px-5 text-sm gap-2',
}

export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: keyof typeof variantStyles
  size?: keyof typeof sizeStyles
}

const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant = 'default', size = 'default', ...props }, ref) => {
    return (
      <button
        ref={ref}
        style={{ borderColor: variant === 'outline' ? 'var(--line)' : undefined }}
        className={cn(
          'inline-flex items-center justify-center rounded-[10px] font-medium whitespace-nowrap',
          'transition-[background-color,border-color,color,filter] duration-150',
          'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 focus-visible:ring-offset-background',
          'disabled:pointer-events-none disabled:opacity-50',
          '[&_svg]:size-[15px] [&_svg]:shrink-0',
          variantStyles[variant],
          sizeStyles[size],
          className,
        )}
        {...props}
      />
    )
  },
)
Button.displayName = 'Button'

export { Button }
