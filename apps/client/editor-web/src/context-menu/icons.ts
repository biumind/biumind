// 右键菜单图标：toolbar ICONS 未覆盖的菜单项在这里补画（同款 svg() 手法，
// currentColor 跟随主题变量，不引新依赖）。

import { svg } from '../toolbar'

export const MENU_ICONS = {
  cut: svg(
    '<circle cx="6" cy="6" r="2.5"/><circle cx="6" cy="18" r="2.5"/>' +
      '<path d="M8.2 7.8 20 19"/><path d="M8.2 16.2 20 5"/>',
  ),
  copy: svg(
    '<rect x="9" y="9" width="11" height="11" rx="2"/>' +
      '<path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>',
  ),
  paste: svg(
    '<rect x="5" y="4" width="14" height="17" rx="2"/>' +
      '<path d="M9 4.5V3a1.5 1.5 0 0 1 1.5-1.5h3A1.5 1.5 0 0 1 15 3v1.5"/><path d="M9 11h6M9 15h6"/>',
  ),
  pastePlain: svg(
    '<rect x="5" y="4" width="14" height="17" rx="2"/>' +
      '<path d="M9 4.5V3a1.5 1.5 0 0 1 1.5-1.5h3A1.5 1.5 0 0 1 15 3v1.5"/><path d="M9 12h6M9 16h4"/>',
  ),
  selectAll: svg(
    '<rect x="4" y="4" width="16" height="16" rx="2" stroke-dasharray="3 2.5"/>' +
      '<path d="m9 12 2 2 4-4.5"/>',
  ),
  openLink: svg(
    '<path d="M14 4h6v6"/><path d="M20 4 11 13"/>' +
      '<path d="M19 13.5V18a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2h4.5"/>',
  ),
  link: svg(
    '<path d="M10 14a4.5 4.5 0 0 0 6.4.4l3-3a4.5 4.5 0 0 0-6.4-6.4l-1.5 1.5"/>' +
      '<path d="M14 10a4.5 4.5 0 0 0-6.4-.4l-3 3a4.5 4.5 0 0 0 6.4 6.4l1.5-1.5"/>',
  ),
  unlink: svg(
    '<path d="M10 14a4.5 4.5 0 0 0 6.4.4l3-3a4.5 4.5 0 0 0-6.4-6.4l-1.5 1.5"/>' +
      '<path d="M14 10a4.5 4.5 0 0 0-6.4-.4l-3 3a4.5 4.5 0 0 0 6.4 6.4l1.5-1.5"/>' +
      '<path d="M4 4l16 16"/>',
  ),
  trash: svg(
    '<path d="M4 7h16"/><path d="M10 11v6M14 11v6"/>' +
      '<path d="M6 7l1 12a2 2 0 0 0 2 2h6a2 2 0 0 0 2-2l1-12"/>' +
      '<path d="M9 7V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v3"/>',
  ),
  image: svg(
    '<rect x="3" y="5" width="18" height="14" rx="2"/>' +
      '<circle cx="9" cy="10" r="1.6"/><path d="m5 18 5-5 3 3 3.5-3.5L21 17"/>',
  ),
  imageReplace: svg(
    '<rect x="3" y="5" width="18" height="14" rx="2"/>' +
      '<circle cx="9" cy="10" r="1.6"/><path d="m5 18 5-5 3 3 3.5-3.5L21 17"/>' +
      '<path d="M15 2.5 18 5l-3 2.5"/>',
  ),
  caption: svg(
    '<rect x="3" y="5" width="18" height="14" rx="2"/>' +
      '<path d="M7 22h10" stroke="none"/>' +
      '<path d="M7 15.5h6M7 12.5h10" transform="translate(0 1)"/>',
  ),
  copyCode: svg(
    '<polyline points="9 8 5 12l4 4"/><polyline points="15 8 19 12l-4 4"/>',
  ),
  ai: svg(
    '<path d="M12 3l1.9 5.6L19.5 10l-5.6 1.9L12 17.5l-1.9-5.6L4.5 10l5.6-1.4z"/>' +
      '<path d="M18.5 15.5l.9 2.6 2.6.9-2.6.9-.9 2.6-.9-2.6-2.6-.9 2.6-.9z"/>',
  ),
} as const
