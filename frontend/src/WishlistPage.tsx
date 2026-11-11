import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { ownerPath, request, reservationPath, sharedProfilePath, sharePath } from './api'
import type { Gift, GiftSort, Summary, Wishlist } from './api'
import { Alert, AvatarFile, CardForm, DetailsForm, formatDate, formatInstant, GiftSorting, Modal, PageButtons, PageStatus, phaseLabels, useAction, usePagedResource } from './ui'
import { GiftCard } from './GiftCard'
import { Avatar, Icon, Illustration } from './visuals'

type Dialog = 'share' | 'settings' | 'add' | 'copy' | 'archive' | Gift

export function WishlistPage({ path }: { path: string }) {
  const [sort, setSort] = useState<GiftSort>('priority')
  const page = usePagedResource<Wishlist>(path + '?sort=' + sort)
  const { value: list, error, refresh } = page
  const action = useAction(refresh)
  const [dialog, setDialog] = useState<Dialog>()
  if (!list || error) return <>
    <Alert message={error} />
    <PageStatus {...page} />
    <PageButtons {...page} />
  </>

  const base = ownerPath(list.id)
  const archived = list.phase === 'archived'
  const editable = list.is_owner && list.phase === 'editing'
  const outcomes = list.is_owner && archived
  const open = list.phase === 'editing' || list.phase === 'holiday'
  const canBook = !list.is_owner && open
  const date = list.holiday_date ? <time dateTime={list.holiday_date ?? undefined}>{formatDate(list.holiday_date)}</time> : 'Без даты'
  const zone = list.time_zone === 'Europe/Moscow' ? 'Москва' : list.time_zone

  return <div className={`wishlist-page stack ${list.is_owner ? 'owner-view' : 'shared-view'}${outcomes ? ' archive-view' : ''}`}>
    {list.is_owner ? <>
      <Link to={archived ? '/?tab=archive' : '/'} className="back-link"><Icon name="back" />Мои списки</Link>
      <header className="panel owner-hero">
        <div className="owner-intro">
          <div className="page-head">
            {archived ? <div className="row archive-title">
              {list.avatar_url && <Avatar name={list.title} url={list.avatar_url} />}
              <h1>{list.title}</h1>
              <span className="badge archived">{phaseLabels[list.phase]}</span>
            </div> : <>
              <span className="badge active-badge">{phaseLabels[list.phase]}</span>
              <div className="row wishlist-title">
                {list.avatar_url && <Avatar name={list.title} url={list.avatar_url} />}
                <h1>{list.title}</h1>
              </div>
            </>}
            <p className="row muted wishlist-meta">
              {archived ? <Icon name="calendar" /> : null}{date}{!archived && zone && <> · {zone}</>}
            </p>
            {open && path === sharePath(list.share_token) && <Link to={sharedProfilePath(list.share_token)}>Профиль для друзей</Link>}
          </div>
          {open && <div className="row owner-actions">
            <button className="secondary" onClick={() => setDialog('share')}><Icon name="share" />Поделиться</button>
            <button className="secondary" onClick={() => setDialog('archive')}>Архивировать</button>
            {editable && <button className="secondary" onClick={() => setDialog('settings')}><Icon name="settings" />Настройки</button>}
          </div>}
        </div>
        {!archived && <div className="owner-art"><Illustration name="gift-occasion" eager /></div>}
      </header>
    </> : <header className="panel shared-hero page-head">
      <Link className="shared-owner-link" to={sharedProfilePath(list.share_token)}>
        <Avatar name={list.owner_name} />
        <span className="shared-owner-name">{list.owner_name}</span>
        <span aria-hidden="true">→</span>
      </Link>
      <div className="row wishlist-title">
        {list.avatar_url && <Avatar name={list.title} url={list.avatar_url} />}
        <h1>{list.title}</h1>
      </div>
      <p className="muted">{list.holiday_date && <>{date} · </>}Выберите подарок и забронируйте его.</p>
    </header>}

    <Alert message={error || action.error} />
    <section className="gift-section stack">
      <header className="row spread section-heading">
        <div className="page-head">
          <h2>{outcomes ? 'Итоги' : 'Подарки'}</h2>
          {outcomes && <p className="muted">Здесь можно исправить итог каждого подарка.</p>}
        </div>
        <div className="row">
          <GiftSorting value={sort} onChange={(next) => { page.first(); setSort(next) }} />
          {editable && <button onClick={() => setDialog('add')}><Icon name="plus" />Добавить подарок</button>}
          {outcomes && <button onClick={() => setDialog('copy')}>Скопировать неподаренные</button>}
        </div>
      </header>
      {canBook && <p className="muted booking-note">Владелец списка не видит, какие подарки забронированы.</p>}
      {list.gifts.length === 0 ? <div className="panel empty page-head">
        <h3>{editable ? 'Первое желание' : 'В списке пока нет подарков'}</h3>
        {editable && <p className="muted">Добавьте подарок, которым хотите поделиться с близкими.</p>}
      </div> : <ul className="card-grid gift-grid">
        {list.gifts.map((gift) => {
          const giftPath = base + '/gifts/' + gift.id
          const bookingPath = reservationPath(list.share_token, gift.id)
          return <GiftCard key={gift.id} gift={gift} view={archived ? 'archive' : list.is_owner ? 'owner' : 'shared'}
            menu={editable && <button className="icon-button gift-more" aria-label={`Действия с подарком «${gift.title}»`}
              onClick={() => setDialog(gift)}><Icon name="more" /></button>}
            booking={canBook ? {
              busy: action.busy,
              reserve: () => void action.run(() => request(bookingPath, { method: 'POST' })),
              cancel: () => void action.run(() => request(bookingPath, { method: 'DELETE' })),
            } : undefined}
            outcome={outcomes && <div className={`outcome ${gift.gifted ? 'success' : 'neutral'}`}>
              <select aria-label={`Итог для подарка «${gift.title}»`} value={String(Boolean(gift.gifted))} disabled={action.busy} onChange={(event) =>
                void action.run(() => request(giftPath + '/outcome', {
                  method: 'PUT', body: { gifted: event.target.value === 'true' },
                }))}>
                <option value="true">Подарено</option>
                <option value="false">Не подарено</option>
              </select>
              <Icon name="down" />
            </div>} />
        })}
      </ul>}
      <PageButtons {...page} disabled={action.busy} />
      {open && list.close_at && <p className="muted closing-note">
        Бронирование до {formatInstant(list.close_at, list.time_zone)} · {zone}
      </p>}
    </section>
    {dialog && <WishlistDialog list={list} dialog={dialog} refresh={refresh} onClose={() => setDialog(undefined)} />}
  </div>
}

function WishlistDialog({ list, dialog, refresh, onClose }: {
  list: Wishlist
  dialog: Dialog
  refresh: () => void
  onClose: () => void
}) {
  const action = useAction(refresh)
  const navigate = useNavigate()
  const base = ownerPath(list.id)
  const titles = {
    share: 'Поделиться списком',
    settings: 'Настройки списка',
    add: 'Добавить подарок',
    copy: 'Скопировать неподаренные',
    archive: 'Архивировать список?'
  }
  const gift = typeof dialog === 'object' ? dialog : undefined
  const save = (path: string, method: 'PUT' | 'POST' | 'DELETE', body?: unknown) => void action.run(async () => {
    await request(path, { method, body })
    onClose()
  })

  return <Modal title={typeof dialog === 'string' ? titles[dialog] : 'Редактировать подарок'}
    busy={action.busy} onClose={onClose}>
    <Alert message={action.error} />
    {dialog === 'archive' && <>
      <p>Редактирование и бронирование закроются. Текущие брони перейдут в историю, а итоги станут доступны в архиве.</p>
      <div className="row">
        <button className="secondary" disabled={action.busy} onClick={onClose}>Отмена</button>
        <button disabled={action.busy} onClick={() => save(base + '/archive', 'POST')}>Архивировать список</button>
      </div>
    </>}
    {dialog === 'share' && <ShareLink token={list.share_token} />}
    {dialog === 'settings' && <>
      <div className="stack wishlist-avatar-settings">
        {list.photo_uploads_enabled && <AvatarFile label={list.avatar_url ? 'Заменить аватарку списка' : 'Загрузить аватарку списка'}
          busy={action.busy} onUpload={(body) => void action.run(() => request(base + '/avatar', { method: 'PUT', body }))} />}
        {list.avatar_url && <button className="secondary" disabled={action.busy} onClick={() =>
          void action.run(() => request(base + '/avatar', { method: 'DELETE' }))}>Удалить аватарку списка</button>}
      </div>
      <DetailsForm initial={list} busy={action.busy} label="Сохранить список" submit={(details) => save(base, 'PUT', details)} />
    </>}
    {dialog === 'copy' && <DetailsForm initial={{ time_zone: list.time_zone }} busy={action.busy} label="Создать новый список"
      submit={(details) => void action.run(async () => {
        const next = await request<Summary>(base + '/copy', { method: 'POST', body: details })
        navigate(ownerPath(next.id))
      })} />}
    {dialog === 'add' && <CardForm busy={action.busy} uploadsEnabled={list.photo_uploads_enabled}
      submit={(card) => save(base + '/gifts', 'POST', card)} />}
    {gift && <>
      <CardForm initial={gift} busy={action.busy} uploadsEnabled={list.photo_uploads_enabled}
        submit={(card) => save(base + '/gifts/' + gift.id, 'PUT', card)} />
      <button className="secondary" disabled={action.busy} onClick={() => save(base + '/gifts/' + gift.id, 'DELETE')}>
        Скрыть подарок
      </button>
    </>}
  </Modal>
}

function ShareLink({ token }: { token: string }) {
  const [status, setStatus] = useState('')
  const url = window.location.origin + sharePath(token)
  async function copyLink() {
    try {
      await navigator.clipboard.writeText(url)
      setStatus('Ссылка скопирована')
    } catch {
      setStatus('Выделите и скопируйте ссылку из поля')
    }
  }
  return <div className="form">
    <label>Ссылка для друзей<input className="copy-link" readOnly value={url} title="Нажмите, чтобы скопировать ссылку"
      onFocus={(event) => event.currentTarget.select()}
      onClick={(event) => { event.currentTarget.select(); void copyLink() }}
      onKeyDown={(event) => {
        if (event.key === 'Enter' || event.key === ' ') {
          event.preventDefault()
          void copyLink()
        }
      }} /></label>
    <p className="muted">Ссылка открывает список подарков. Из него друзья смогут перейти в ваш профиль.</p>
    <div className="row">
      <button onClick={() => void copyLink()}>Скопировать ссылку</button>
      <span className="muted" role="status">{status}</span>
    </div>
  </div>
}
