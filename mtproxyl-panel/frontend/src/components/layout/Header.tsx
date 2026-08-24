import { RefreshCw } from 'lucide-react';
import { cn } from '@/lib/utils';

interface HeaderProps {
  title: string;
  refreshing?: boolean;
  onRefresh?: () => void;
}

export function Header({ title, refreshing, onRefresh }: HeaderProps) {
  return (
    <header className="h-14 flex items-center justify-between px-4 lg:px-6">
      <h2 className="text-[17px] font-[640] tracking-[-0.02em] text-text-primary">{title}</h2>
      {onRefresh && (
        <button
          onClick={onRefresh}
          className="p-2 rounded-[10px] text-text-secondary hover:text-text-primary hover:bg-surface-hover transition-colors"
          title="Обновить"
        >
          <RefreshCw size={16} className={cn(refreshing && 'animate-spin')} />
        </button>
      )}
    </header>
  );
}
