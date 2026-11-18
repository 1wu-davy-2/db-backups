import { useId, useLayoutEffect, useRef } from 'react'
import type { ReactNode } from 'react'

interface Props {
  title: string
  onClose: () => void
  children: ReactNode
  footer?: ReactNode
  busy?: boolean
}

function Overlay({
  title, subtitle, onClose, children, footer, busy = false, drawer = false,
}: Props & { subtitle?: string; drawer?: boolean }) {
  const ref = useRef<HTMLDialogElement>(null)
  const titleID = useId()

  useLayoutEffect(() => {
    const dialog = ref.current!
    dialog.showModal()
    return () => dialog.close()
  }, [])

  return (
    <dialog
      ref={ref}
      className="mask"
      aria-labelledby={titleID}
      aria-busy={busy}
      onCancel={(e) => {
        e.preventDefault()
        if (!busy) onClose()
      }}
      onMouseDown={(e) => {
        if (e.target === e.currentTarget && !busy) onClose()
      }}
    >
      <section className={drawer ? 'drawer' : 'modal'}>
        <div className={drawer ? 'drawer-head' : 'modal-head'}>
          <div>
            <div className="modal-title" id={titleID}>{title}</div>
            {subtitle && <div className="faint small">{subtitle}</div>}
          </div>
          <button className="btn btn-sm" type="button" disabled={busy} onClick={onClose}>关闭</button>
        </div>
        <div className={drawer ? 'drawer-body' : 'modal-body'}>{children}</div>
        {footer && <div className="modal-foot">{footer}</div>}
      </section>
    </dialog>
  )
}

export function Modal(props: Props) {
  return <Overlay {...props} />
}

export function Drawer(props: Props & { subtitle?: string }) {
  return <Overlay {...props} drawer />
}
