import { useState } from 'react'
import type { ReactNode } from 'react'
import type { Gift } from './api'
import { Modal } from './ui'
import { GiftArt, Icon } from './visuals'

type GiftView = 'archive' | 'owner' | 'shared'

const rubles = new Intl.NumberFormat('ru', { style: 'currency', currency: 'RUB', minimumFractionDigits: 0 })

export function GiftCard({ gift, view, menu, outcome, booking }: {
  gift: Gift
  view: GiftView
  menu?: ReactNode
  outcome?: ReactNode
  booking?: { busy: boolean; reserve: () => void; cancel: () => void }
}) {
  const [open, setOpen] = useState(false)
  const price = gift.price === null ? null : rubles.format(Number(gift.price))
  const storeLink = gift.product_url && <a href={gift.product_url} target="_blank" rel="noopener noreferrer">В магазин <span aria-hidden="true">↗</span></a>

  return <li className={`gift-card${view === 'owner' ? ' owner-card' : ''}`}>
    <GiftImages gift={gift} view={view} />
    <div className="gift-body">
      <div className="gift-heading">
        <h3><button type="button" className="gift-open" aria-haspopup="dialog"
          onClick={() => setOpen(true)}>{gift.title}</button></h3>
        {menu}
      </div>
      {price && <p className="gift-price">{price}</p>}
      {storeLink}
      <div className="row gift-status">
        {!gift.active && <span className="badge neutral">Скрыт владельцем</span>}
        {gift.reservation === 'free' && <span className="badge success">Свободен</span>}
        {gift.reservation === 'taken' && <span className="badge neutral">Занят</span>}
        {gift.reservation === 'mine' && <span className="my-booking"><Icon name="check" />Ваша бронь</span>}
      </div>
      {booking && gift.reservation === 'free' && <div className="gift-action">
        <button disabled={booking.busy} onClick={booking.reserve}>Забронировать</button>
      </div>}
      {booking && gift.reservation === 'mine' && <div className="gift-action">
        <button className="secondary" disabled={booking.busy} onClick={booking.cancel}>Отменить бронь</button>
      </div>}
      {outcome}
    </div>
    {open && <Modal title={gift.title} onClose={() => setOpen(false)}>
      <GiftImages gift={gift} view={view} gallery />
      {price && <p className="gift-price">{price}</p>}
      <p className="description">{gift.description || 'Описание не добавлено.'}</p>
      {storeLink}
    </Modal>}
  </li>
}

export function GiftImages({ gift, view, gallery = false, className = 'gift-art' }: {
  gift: Pick<Gift, 'title' | 'photos'>
  view: GiftView | 'booking'
  gallery?: boolean
  className?: string
}) {
  const [index, setIndex] = useState(0)
  const [failedPhoto, setFailedPhoto] = useState('')
  const photos = gift.photos ?? []
  const current = index % (photos.length || 1)
  const photo = photos[current]
  return <div className={className}>
    {photo && photo.url !== failedPhoto
      ? <img key={photo.id} className="gift-photo" src={photo.url} alt={gallery ? `Фото ${current + 1} подарка «${gift.title}»` : ''}
          loading={gallery ? 'eager' : 'lazy'} onError={() => setFailedPhoto(photo.url)} />
      : <GiftArt title={gift.title} view={view} />}
    {gallery && photos.length > 1 && <div className="photo-controls">
      <button type="button" className="secondary" aria-label="Предыдущее фото"
        onClick={() => setIndex((current + photos.length - 1) % photos.length)}>‹</button>
      <span aria-live="polite">{current + 1} / {photos.length}</span>
      <button type="button" className="secondary" aria-label="Следующее фото"
        onClick={() => setIndex((current + 1) % photos.length)}>›</button>
    </div>}
  </div>
}
