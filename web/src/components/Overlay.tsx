import type { ReactNode } from 'react'

export function Modal({
  title, onClose, children, footer,
}: { title: string; onClose: () => void; children: ReactNode; footer?: ReactNode }) {
  return (
    <div
      className="mask"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose()
      }}
    >
      <div className="modal" role="dialog" aria-modal="true">
        <div className="modal-head">
          <span className="modal-title">{title}</span>
          <button className="btn btn-sm" onClick={onClose}>关闭</button>
        </div>
        <div className="modal-body">{children}</div>
        {footer && <div className="modal-foot">{footer}</div>}
      </div>
    </div>
  )
}

export function Drawer({
  title, subtitle, onClose, children, footer,
}: {
  title: string
  subtitle?: string
  onClose: () => void
  children: ReactNode
  footer?: ReactNode
}) {
  return (
    <>
      <div className="mask" style={{ zIndex: 55 }} onMouseDown={onClose} />
      <div className="drawer" role="dialog" aria-modal="true">
        <div className="drawer-head">
          <div>
            <div className="modal-title">{title}</div>
            {subtitle && <div className="faint small">{subtitle}</div>}
          </div>
          <button className="btn btn-sm" onClick={onClose}>关闭</button>
        </div>
        <div className="drawer-body">{children}</div>
        {footer && <div className="modal-foot">{footer}</div>}
      </div>
    </>
  )
}
