/**
 * Inline icons.
 *
 * Drawn here rather than pulled from a package: the set is small, and a strict
 * asset policy means everything the window renders has to be part of the bundle
 * anyway.
 */

type IconProps = {
  size?: number;
  className?: string;
};

const base = (size: number) => ({
  width: size,
  height: size,
  viewBox: "0 0 24 24",
  fill: "none" as const,
  stroke: "currentColor",
  strokeWidth: 1.75,
  strokeLinecap: "round" as const,
  strokeLinejoin: "round" as const,
  "aria-hidden": true,
});

/**
 * BrandMark is the app's logo.
 *
 * A bold white chevron on a blue tile. It reads two ways on purpose: as the V of
 * VPN, and as a funnel narrowing to a point -- traffic gathered into one
 * protected path. The design constraint was the 16px favicon and tray icon, so
 * it is a single high-contrast silhouette with no interior detail to lose: at
 * small sizes a shield-with-a-keyhole turns to mush, whereas this stays legible.
 *
 * The same geometry is drawn in Go for the tray and .ico -- see internal/brand.
 * Change one and change the other.
 */
export const BrandMark = ({ size = 32, className }: IconProps) => (
  <svg
    width={size}
    height={size}
    viewBox="0 0 32 32"
    className={className}
    aria-hidden
  >
    <rect width="32" height="32" rx="7.04" fill="#2563eb" />
    <path
      d="M8.32 10.88 L16 21.76 L23.68 10.88"
      fill="none"
      stroke="#ffffff"
      strokeWidth="5.44"
      strokeLinecap="round"
      strokeLinejoin="round"
    />
  </svg>
);

export const ShieldIcon = ({ size = 18, className }: IconProps) => (
  <svg {...base(size)} className={className}>
    <path d="M12 3l7 3v6c0 4.2-2.9 7.6-7 9-4.1-1.4-7-4.8-7-9V6l7-3z" />
  </svg>
);

export const ShieldCheckIcon = ({ size = 18, className }: IconProps) => (
  <svg {...base(size)} className={className}>
    <path d="M12 3l7 3v6c0 4.2-2.9 7.6-7 9-4.1-1.4-7-4.8-7-9V6l7-3z" />
    <path d="M9 12l2 2 4-4" />
  </svg>
);

export const PowerIcon = ({ size = 26, className }: IconProps) => (
  <svg {...base(size)} className={className}>
    <path d="M12 3v9" />
    <path d="M7.5 6.6a7.5 7.5 0 109 0" />
  </svg>
);

export const PlusIcon = ({ size = 16, className }: IconProps) => (
  <svg {...base(size)} className={className}>
    <path d="M12 5v14M5 12h14" />
  </svg>
);

export const SettingsIcon = ({ size = 17, className }: IconProps) => (
  <svg {...base(size)} className={className}>
    <circle cx="12" cy="12" r="3" />
    <path d="M19.4 15a1.7 1.7 0 00.3 1.9l.1.1a2 2 0 11-2.8 2.8l-.1-.1a1.7 1.7 0 00-1.9-.3 1.7 1.7 0 00-1 1.5V21a2 2 0 11-4 0v-.1a1.7 1.7 0 00-1.1-1.5 1.7 1.7 0 00-1.9.3l-.1.1a2 2 0 11-2.8-2.8l.1-.1a1.7 1.7 0 00.3-1.9 1.7 1.7 0 00-1.5-1H3a2 2 0 110-4h.1a1.7 1.7 0 001.5-1.1 1.7 1.7 0 00-.3-1.9l-.1-.1a2 2 0 112.8-2.8l.1.1a1.7 1.7 0 001.9.3H9a1.7 1.7 0 001-1.5V3a2 2 0 114 0v.1a1.7 1.7 0 001 1.5 1.7 1.7 0 001.9-.3l.1-.1a2 2 0 112.8 2.8l-.1.1a1.7 1.7 0 00-.3 1.9V9a1.7 1.7 0 001.5 1H21a2 2 0 110 4h-.1a1.7 1.7 0 00-1.5 1z" />
  </svg>
);

export const MoreIcon = ({ size = 16, className }: IconProps) => (
  <svg {...base(size)} className={className}>
    <circle cx="12" cy="5.5" r="1.4" fill="currentColor" stroke="none" />
    <circle cx="12" cy="12" r="1.4" fill="currentColor" stroke="none" />
    <circle cx="12" cy="18.5" r="1.4" fill="currentColor" stroke="none" />
  </svg>
);

export const ChevronIcon = ({ size = 16, className }: IconProps) => (
  <svg {...base(size)} className={className}>
    <path d="M9 18l6-6-6-6" />
  </svg>
);

export const TrashIcon = ({ size = 15, className }: IconProps) => (
  <svg {...base(size)} className={className}>
    <path d="M4 7h16M9 7V5h6v2M6 7l1 13h10l1-13" />
  </svg>
);

export const PencilIcon = ({ size = 15, className }: IconProps) => (
  <svg {...base(size)} className={className}>
    <path d="M4 20h4l10-10-4-4L4 16v4z" />
  </svg>
);

export const KeyIcon = ({ size = 15, className }: IconProps) => (
  <svg {...base(size)} className={className}>
    <circle cx="8" cy="14" r="4" />
    <path d="M11 11l7-7 3 3-2 2 2 2-3 3-2-2-2 2" />
  </svg>
);

export const FolderIcon = ({ size = 15, className }: IconProps) => (
  <svg {...base(size)} className={className}>
    <path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2V7z" />
  </svg>
);

export const AlertIcon = ({ size = 16, className }: IconProps) => (
  <svg {...base(size)} className={className}>
    <path d="M12 4l9 16H3l9-16z" />
    <path d="M12 10v4M12 17.2v.1" />
  </svg>
);

export const CheckIcon = ({ size = 16, className }: IconProps) => (
  <svg {...base(size)} className={className}>
    <path d="M4 12.5l5 5L20 6.5" />
  </svg>
);

export const DownloadIcon = ({ size = 26, className }: IconProps) => (
  <svg {...base(size)} className={className}>
    <path d="M12 4v11M7.5 10.5L12 15l4.5-4.5" />
    <path d="M5 19h14" />
  </svg>
);

export const ArrowDownIcon = ({ size = 14, className }: IconProps) => (
  <svg {...base(size)} className={className}>
    <path d="M12 5v14M6.5 13.5L12 19l5.5-5.5" />
  </svg>
);

export const ArrowUpIcon = ({ size = 14, className }: IconProps) => (
  <svg {...base(size)} className={className}>
    <path d="M12 19V5M6.5 10.5L12 5l5.5 5.5" />
  </svg>
);

export const CloseIcon = ({ size = 16, className }: IconProps) => (
  <svg {...base(size)} className={className}>
    <path d="M6 6l12 12M18 6L6 18" />
  </svg>
);
