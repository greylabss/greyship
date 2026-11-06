import {
  ArrowRight,
  ArrowUpRight,
  Bell,
  BookOpen,
  ChartColumn,
  ChartLine,
  Check,
  ChevronDown,
  ChevronsUpDown,
  Copy,
  BadgeCheck,
  Cpu,
  SquareTerminal,
  CreditCard,
  Ellipsis,
  GitBranch,
  GitCommitHorizontal,
  Globe,
  House,
  KeyRound,
  LayoutGrid,
  List,
  ListFilter,
  Menu,
  PanelLeft,
  Plus,
  ScrollText,
  Search,
  Settings,
  TriangleAlert,
  X,
} from "lucide-react";
import type { SVGProps } from "react";

// One set: lucide (the shadcn default), sized 16px in globals.css. GitHub is drawn here because lucide has no brand marks.
type IconProps = SVGProps<SVGSVGElement>;

export const icons = {
  projects: LayoutGrid,
  usage: ChartColumn,
  overview: House,
  deployments: GitCommitHorizontal,
  logs: ScrollText,
  metrics: ChartLine,
  runtime: Cpu,
  env: KeyRound,
  domains: Globe,
  settings: Settings,
  copy: Copy,
  check: Check,
  chevronDown: ChevronDown,
  sidebar: PanelLeft,
  search: Search,
  plus: Plus,
  external: ArrowUpRight,
  close: X,
  alert: TriangleAlert,
  github: (p: IconProps) => (
    <svg width={16} height={16} viewBox="0 0 16 16" fill="currentColor" aria-hidden="true" {...p}>
      <path d="M8 .2a8 8 0 0 0-2.53 15.59c.4.07.55-.17.55-.38v-1.33c-2.23.48-2.7-1.07-2.7-1.07-.36-.92-.89-1.17-.89-1.17-.73-.5.05-.49.05-.49.8.06 1.23.83 1.23.83.72 1.22 1.88.87 2.34.66.07-.52.28-.87.5-1.07-1.78-.2-3.65-.89-3.65-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82a7.6 7.6 0 0 1 4 0c1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.28.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48v2.2c0 .21.15.46.55.38A8 8 0 0 0 8 .2Z" />
    </svg>
  ),
  branch: GitBranch,
  bell: Bell,
  docs: BookOpen,
  grid: LayoutGrid,
  list: List,
  sort: ListFilter,
  updown: ChevronsUpDown,
  billing: CreditCard,
  more: Ellipsis,
  menu: Menu,
  terminal: SquareTerminal,
  betaTester: BadgeCheck,
  arrowRight: ArrowRight,
};

export type IconName = keyof typeof icons;
