import { useState } from 'react'
import type { ReactNode } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { cancelBookingPath, ownerPath, request, requestPage, sharePath } from './api'
import type { Booking, HistoryEntry, Summary } from './api'
import { GiftImages } from './GiftCard'
import { Alert, DetailsForm, formatDate, formatGiftCount, formatInstant, Modal, PageButtons, PageStatus, phaseLabels, useAction, usePagedResource, useResource } from './ui'
import { Icon, OccasionArt } from './visuals'

const shortMonth = new Intl.DateTimeFormat('ru', { month: 'short', timeZone: 'UTC' })

export async function loadOwnedLists(signal: AbortSignal) {
  const lists = new Map<string, Summary>()
  let cursor = ''
  do {
    const { data, next } = await requestPage<Summary[] | null>('/wishlists', cursor, signal)
    for (const list of data ?? []) lists.set(list.id, list)
    cursor = next ?? ''
  } while (cursor)
  return [...lists.values()]
}

export function ListsPage() {
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const archived = params.get('tab') === 'archive'
  const [creating, setCreating] = useState(false)
  const page = useResource(loadOwnedLists)
  const lists = page.value?.filter((list) => (list.phase === 'archived') === archived)
  const archivedCount = page.value?.filter((list) => list.phase === 'archived').length ?? 0
  const featured = !archived ? lists?.find((list) =>
    list.holiday_date && (list.phase === 'editing' || list.phase === 'holiday')) : undefined
  const otherLists = lists?.filter((list) => list !== featured)

  return <section className="lists-page stack">
    <header className="row spread page-heading">
      <div className="page-head">
        <h1>Мои списки</h1>
        <p className="muted">Собирайте желания к праздникам и делитесь ими с близкими.</p>
      </div>
      <button onClick={() => setCreating(true)}><Icon name="plus" />Создать список</button>
    </header>
    <div className="row filters page-tabs" role="group" aria-label="Статус списков">
      <button aria-pressed={!archived} onClick={() => setParams({})}>
        Активные {page.value && <span className="muted">{page.value.length - archivedCount}</span>}
      </button>
      <button aria-pressed={archived} onClick={() => setParams({ tab: 'archive' })}>
        Архив {page.value && <span className="muted">{archivedCount}</span>}
      </button>
    </div>
    <Alert message={page.error} />
    {lists === undefined ? <PageStatus {...page} /> : lists.length ? <div className="occasion-layout">
      {featured && <Link className="occasion-feature" to={ownerPath(featured.id)}>
        <div className="occasion-copy">
          <p className="eyebrow"><Icon name="calendar" />{featured.phase === 'holiday' ? phaseLabels.holiday : 'Ближайший повод'}</p>
          <div className="page-head">
            <h2>{featured.title}</h2>
            <p className="muted">
              <time dateTime={featured.holiday_date ?? undefined}>{formatDate(featured.holiday_date)}</time>
              {' · '}{formatGiftCount(featured.gift_count)}
            </p>
          </div>
          <span className="occasion-action">Открыть список <Icon name="forward" /></span>
        </div>
        <div className={`occasion-art${featured.avatar_url ? ' has-cover' : ''}`}>
          <OccasionArt title={featured.title} url={featured.avatar_url} />
        </div>
      </Link>}
      {!!otherLists?.length && <ul className="occasion-list">
        {otherLists.map((list) => <li key={list.id}><OccasionTicket list={list} /></li>)}
      </ul>}
    </div> : <p className="panel muted empty">{archived ? 'В архиве пока нет списков.' : 'У вас пока нет активных списков.'}</p>}
    {creating && <CreateListDialog onClose={() => setCreating(false)} onCreate={(list) => navigate(ownerPath(list.id))} />}
  </section>
}

function OccasionTicket({ list }: { list: Summary }) {
  const date = list.holiday_date
  return <Link className="occasion-ticket" to={ownerPath(list.id)}>
    {date ? <time className="date-stamp" dateTime={date} aria-label={formatDate(date)}>
      <strong>{Number(date.slice(-2))}</strong>
      <small>{shortMonth.format(new Date(date + 'T00:00:00Z')).replace(/\.$/u, '')}</small>
    </time> : <span className="date-stamp"><Icon name="calendar" /><small>Без даты</small></span>}
    <div className="page-head">
      <h2>{list.title}</h2>
      <p className="muted">
        {list.phase === 'archived' && date && <>{date.slice(0, 4)} · </>}
        {formatGiftCount(list.gift_count)}
        {list.phase !== 'editing' && list.phase !== 'archived' && ` · ${phaseLabels[list.phase]}`}
      </p>
    </div>
    {list.avatar_url ? <span className="occasion-thumbnail"><OccasionArt title={list.title} url={list.avatar_url} /></span> : <Icon name="forward" />}
  </Link>
}

function CreateListDialog({ onClose, onCreate }: { onClose: () => void; onCreate: (list: Summary) => void }) {
  const action = useAction()
  return <Modal title="Создать список" onClose={onClose} busy={action.busy}>
    <Alert message={action.error} />
    <DetailsForm busy={action.busy} label="Создать список" submit={(details) => void action.run(async () => {
      onCreate(await request<Summary>('/wishlists', { method: 'POST', body: details }))
    })} />
  </Modal>
}

export function BookingsPage() {
  const [params, setParams] = useSearchParams()
  const history = params.get('tab') === 'history'

  return <section className="bookings-page stack">
    <h1>Брони и история</h1>
    <div className="row filters page-tabs" role="group" aria-label="Брони и история">
      <button aria-pressed={!history} onClick={() => setParams({})}>Текущие брони</button>
      <button aria-pressed={history} onClick={() => setParams({ tab: 'history' })}>История подарков</button>
    </div>
    {history ? <GiftHistory /> : <CurrentBookings />}
  </section>
}

function ActivityRow({ entry, list, children }: {
  entry: Booking | HistoryEntry
  list: ReactNode
  children: ReactNode
}) {
  return <li className="activity-row">
    <GiftImages gift={{ title: entry.gift_title, photos: entry.photos }} view="booking" className="activity-art" />
    <div className="activity-copy">
      <h2>{entry.gift_title}</h2>
      <p className="muted" title={`Бронь от ${formatInstant(entry.reserved_at)}`}>{entry.recipient} · {list}</p>
    </div>
    <div className="activity-actions row">{children}</div>
  </li>
}

function CurrentBookings() {
  const page = usePagedResource<Booking[] | null>('/reservations')
  const action = useAction(page.refresh)

  return <>
    <Alert message={page.error || action.error} />
    {page.value === undefined ? <PageStatus {...page} /> : page.value?.length ? <ul className="activity-list">
      {page.value.map((booking) => <ActivityRow key={booking.gift_id} entry={booking} list={<>
        {booking.share_token ? <Link to={sharePath(booking.share_token)}>{booking.wishlist_title}</Link> : booking.wishlist_title}
        {!booking.active && ' · Подарок скрыт'}
      </>}>
        <button className="secondary" disabled={!booking.can_cancel || action.busy} onClick={() => void action.run(() =>
          request(cancelBookingPath(booking.gift_id), { method: 'DELETE' }))}>
          {booking.can_cancel ? 'Отменить бронь' : 'Отмена закрыта'}
        </button>
      </ActivityRow>)}
    </ul> : <p className="panel muted empty">У вас нет текущих броней.</p>}
    <PageButtons {...page} disabled={action.busy} />
  </>
}

function GiftHistory() {
  const page = usePagedResource<HistoryEntry[] | null>('/history')
  const action = useAction(page.refresh)

  return <>
    <p className="muted">Здесь появляются брони, которые сохранились к закрытию списка.</p>
    <Alert message={page.error || action.error} />
    {page.value === undefined ? <PageStatus {...page} /> : page.value?.length ? <ul className="activity-list">
      {page.value.map((entry) => <ActivityRow key={entry.gift_id} entry={entry} list={entry.wishlist_title}>
        <span className={entry.gifted ? 'badge success' : 'badge neutral'}>{entry.gifted ? 'Подарено' : 'Не подарено'}</span>
        <button className="secondary" disabled={action.busy} onClick={() => void action.run(() =>
          request('/history/' + entry.gift_id, { method: 'DELETE' }))}>Удалить запись</button>
      </ActivityRow>)}
    </ul> : <p className="panel muted empty">История пока пуста.</p>}
    <PageButtons {...page} disabled={action.busy} />
  </>
}
