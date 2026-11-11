import { useCallback, useEffect, useState } from 'react'
import { Link, useLocation, useNavigate, useSearchParams } from 'react-router-dom'
import { APIError, ownerPath, request } from './api'
import type { Details, Gift, GiftSort, Phase } from './api'
import { loadOwnedLists } from './Dashboard'
import { DateField } from './DateField'
import { Alert, DetailsForm, formatDate, formatInstant, GiftSorting, Modal, PageButtons, PageStatus, useAction, usePagedResource, useResource } from './ui'
import { GiftCard } from './GiftCard'
import { Avatar, Icon } from './visuals'

const eventsPath = '/santa/events'
const eventPath = (id: string) => eventsPath + '/' + id
const invitePath = (token: string) => '/santa/invitations/' + encodeURIComponent(token)

type ApplicationStatus = 'pending' | 'approved' | 'rejected' | 'removed'
const applicationLabels: Record<ApplicationStatus, string> = {
  pending: 'Заявка на рассмотрении',
  approved: 'Участвует',
  rejected: 'Заявка отклонена',
  removed: 'Исключён из состава',
}

interface SantaEvent {
  id: string
  title: string
  exchange_date: string
  time_zone: string
  close_at: string
  budget: string
  currency: string
  rules: string
  round: number
  closed: boolean
  is_organizer: boolean
  status?: ApplicationStatus
}

interface Application {
  user_id: string
  name: string
  status: ApplicationStatus
}

interface EventView extends SantaEvent {
  version: string
  wishlist_id?: string
  recipient?: { name: string; wishlist_id?: string }
  applications?: Application[]
  roster_changed?: boolean
}

type InvitationLink = { invite_token: string }

const loadEvents = (signal: AbortSignal) => request<SantaEvent[]>(eventsPath, { signal })

export function SantaEventsPage() {
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const archived = params.get('tab') === 'archive'
  const [creating, setCreating] = useState(false)
  const page = useResource(loadEvents)
  const events = page.value?.filter((event) => event.closed === archived)

  return <section className="lists-page stack">
    <header className="row spread page-heading">
      <div className="page-head">
        <h1>Тайный Санта</h1>
        <p className="muted">Организуйте обмен подарками или присоединитесь по приглашению.</p>
      </div>
      <button onClick={() => setCreating(true)}><Icon name="plus" />Создать событие</button>
    </header>
    <div className="row filters page-tabs" role="group" aria-label="Статус событий">
      <button aria-pressed={!archived} onClick={() => setParams({})}>Активные</button>
      <button aria-pressed={archived} onClick={() => setParams({ tab: 'archive' })}>Архив</button>
    </div>
    <Alert message={page.error} />
    {events === undefined ? <PageStatus {...page} /> : events.length ? <ul className="card-grid">
      {events.map((event) => <li key={event.id}>
        <Link className="list-card santa-event-card" to={eventPath(event.id)}>
          <span className="badge occasion-status">{event.is_organizer ? 'Организатор' : applicationLabels[event.status!]}</span>
          <h2>{event.title}</h2>
          <time dateTime={event.exchange_date} className="muted">{formatDate(event.exchange_date)}</time>
          {event.closed && event.round === 0 && <span>Жеребьёвка не проведена</span>}
        </Link>
      </li>)}
    </ul> : <p className="panel muted empty">{archived ? 'В архиве пока нет событий.' : 'У вас пока нет активных событий.'}</p>}
    {creating && <CreateEventDialog onClose={() => setCreating(false)} onCreate={(event) => {
      setCreating(false)
      navigate(eventPath(event.id), { state: { inviteToken: event.invite_token } })
    }} />}
  </section>
}

function CreateEventDialog({ onClose, onCreate }: {
  onClose: () => void
  onCreate: (event: InvitationLink & { id: string }) => void
}) {
  const action = useAction()
  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'

  return <Modal title="Создать событие" onClose={onClose} busy={action.busy}>
    <Alert message={action.error} />
    <form className="form" onSubmit={(e) => {
      e.preventDefault()
      const data = new FormData(e.currentTarget)
      void action.run(async () => onCreate(await request(eventsPath, { method: 'POST', body: {
        title: data.get('title'),
        exchange_date: data.get('date'),
        time_zone: data.get('zone'),
        budget: String(data.get('budget')).trim().replace(',', '.'),
        currency: data.get('currency'),
        rules: data.get('rules'),
        participate: data.has('participate'),
      } })))
    }}>
      <label>Название события<input name="title" required maxLength={150} placeholder="Новогодний обмен" /></label>
      <div className="form-row">
        <DateField label="Дата обмена" name="date" required />
        <label>Часовой пояс IANA<input name="zone" list="time-zones" required defaultValue={zone} /></label>
      </div>
      <div className="form-row">
        <label>Бюджет на подарок<input name="budget" inputMode="decimal" pattern="[0-9]{1,10}([.,][0-9]{1,2})?" required /></label>
        <label>Код валюты<input name="currency" defaultValue="RUB" pattern="[a-zA-Z]{3}" required maxLength={3} /></label>
      </div>
      <label>Правила<textarea name="rules" rows={3} maxLength={5000} placeholder="Как и где вы обменяетесь подарками" /></label>
      <label className="santa-check"><input type="checkbox" name="participate" />Я тоже участвую в обмене</label>
      <button disabled={action.busy}>Создать событие</button>
    </form>
  </Modal>
}

function EventHeading({ event }: { event: SantaEvent }) {
  return <header className="page-head">
    <div className="row">
      <span className={event.closed ? 'badge archived' : 'badge active-badge'}>{event.closed ? 'В архиве' : 'Активное событие'}</span>
      {event.status && <span className="badge neutral">{applicationLabels[event.status]}</span>}
    </div>
    <h1>{event.title}</h1>
    <p className="muted"><time dateTime={event.exchange_date}>{formatDate(event.exchange_date)}</time> · {event.time_zone}</p>
  </header>
}

function EventDetails({ event }: { event: SantaEvent }) {
  const budget = new Intl.NumberFormat('ru', { maximumFractionDigits: 2 }).format(Number(event.budget))
  return <section className="panel santa-panel">
    <h2>Обмен подарками</h2>
    <p>Ориентир по бюджету · {budget} {event.currency}</p>
    {event.rules && <p className="description">{event.rules}</p>}
    <p className="muted">Договоритесь о доставке и обмене подарками самостоятельно.</p>
    {!event.closed && <p className="muted">Событие закроется {formatInstant(event.close_at, event.time_zone)}.</p>}
  </section>
}

export function SantaInvitationPage({ token }: { token: string }) {
  const path = invitePath(token)
  const navigate = useNavigate()
  const load = useCallback((signal: AbortSignal) => request<SantaEvent>(path, { signal }), [path])
  const page = useResource(load)
  const action = useAction(page.refresh)
  const event = page.value

  return <section className="stack">
    <Link className="back-link" to="/santa"><Icon name="back" />Тайный Санта</Link>
    <Alert message={page.error || action.error} />
    {event && !page.error ? <>
      <p className="muted">Приглашение в «Тайного Санту»</p>
      <EventHeading event={event} />
      <EventDetails event={event} />
      <div className="row">
        {!event.closed && !event.status && <button disabled={action.busy} onClick={() => void action.run(async () => {
          const result = await request<{ id: string }>(path + '/applications', { method: 'POST' })
          navigate(eventPath(result.id))
        })}>Подать заявку</button>}
        {(event.status || event.is_organizer) && <Link to={eventPath(event.id)}>Открыть событие</Link>}
        {event.closed && !event.status && !event.is_organizer && <p>Приём заявок завершён.</p>}
      </div>
    </> : <PageStatus {...page} />}
  </section>
}

export function SantaEventPage({ id, userID }: { id: string; userID: string }) {
  const path = eventPath(id)
  const { state } = useLocation()
  const load = useCallback((signal: AbortSignal) => request<EventView>(path, { signal }), [path])
  const page = useResource(load)
  const event = page.value
  const { refresh } = page
  const closed = event?.closed
  useEffect(() => {
    if (closed) return
    const check = () => { if (!document.hidden) refresh() }
    window.addEventListener('focus', check)
    const timer = window.setInterval(check, 30000)
    return () => {
      window.removeEventListener('focus', check)
      window.clearInterval(timer)
    }
  }, [closed, refresh])

  const listID = event?.recipient?.wishlist_id
  const grant = event && !event.closed && listID ? path + '/rounds/' + event.round + '/wishlists/' + listID : undefined
  const step = event?.round === 0 ? 0 : event?.roster_changed ? 1 : 2

  return <section className="stack">
    <Link className="back-link" to={event?.closed ? '/santa?tab=archive' : '/santa'}><Icon name="back" />Тайный Санта</Link>
    <Alert message={page.error} />
    {event && !page.error ? <>
      <EventHeading event={event} />
      {!event.closed && <ol className="santa-steps" aria-label="Этапы обмена подарками">
        {['Сбор участников', 'Жеребьёвка', 'Обмен подарками'].map((label, index) =>
          <li key={label} className={index < step ? 'completed' : undefined} aria-current={index === step ? 'step' : undefined}>
            {index < step && <Icon name="check" />}{label}
          </li>)}
      </ol>}
      {event.roster_changed && !event.closed && <p className="panel empty" role="status">
        Состав изменился. Организатору нужно провести жеребьёвку для всего текущего состава.
      </p>}
      {event.round === 0 && <p className="panel empty">Жеребьёвка не проведена</p>}
      {event.recipient && <section className="panel santa-panel">
        <h2>Ваш получатель</h2>
        {!event.closed && event.round > 1 && <p className="badge active-badge" role="status">
          Прошла новая жеребьёвка. Проверьте своего получателя.
        </p>}
        <p>{event.recipient.name}</p>
        {!event.closed && !grant && <p className="muted">У получателя сейчас нет доступного списка желаний.</p>}
      </section>}
      {grant && <RecipientWishlist key={grant} path={grant} refreshEvent={refresh} />}
      {!event.closed && event.status === 'approved' && <WishlistBinding event={event} refresh={refresh} />}
      <EventDetails event={event} />
      {event.is_organizer && <EventAdmin event={event} userID={userID} initialToken={state?.inviteToken} refresh={refresh} />}
    </> : <PageStatus {...page} />}
  </section>
}

function WishlistBinding({ event, refresh }: { event: EventView; refresh: () => void }) {
  const [dialog, setDialog] = useState<'bind' | 'create'>()
  return <section className="panel santa-panel">
    <h2>Мой список желаний</h2>
    <p className="muted">Ваш Тайный Санта увидит этот список после жеребьёвки. Привязка необязательна.</p>
    <div className="row">
      {event.wishlist_id && <Link to={ownerPath(event.wishlist_id)}>Открыть мой список</Link>}
      <button className="secondary" onClick={() => setDialog('bind')}>{event.wishlist_id ? 'Изменить привязку' : 'Привязать список'}</button>
      <button className="secondary" onClick={() => setDialog('create')}>Создать список</button>
    </div>
    {dialog && <BindingDialog event={event} create={dialog === 'create'} onClose={() => setDialog(undefined)} onSave={() => {
      setDialog(undefined)
      refresh()
    }} />}
  </section>
}

function BindingDialog({ event, create, onClose, onSave }: {
  event: EventView; create: boolean; onClose: () => void; onSave: () => void
}) {
  const action = useAction()
  const navigate = useNavigate()
  const path = eventPath(event.id) + '/wishlist'

  function createList(details: Details) {
    void action.run(async () => {
      const list = await request<{ id: string }>(path, { method: 'POST', body: details })
      onSave()
      navigate(ownerPath(list.id))
    })
  }

  return <Modal title={create ? 'Создать список желаний' : 'Привязать список желаний'} onClose={onClose} busy={action.busy}>
    <Alert message={action.error} />
    {create ? <DetailsForm initial={{ title: event.title, time_zone: event.time_zone }} suggestedDate={event.exchange_date}
      busy={action.busy} label="Создать и привязать" submit={createList} />
      : <SelectWishlist initial={event.wishlist_id} busy={action.busy} onSave={(wishlistID) => void action.run(async () => {
        await request(path, { method: 'PUT', body: { wishlist_id: wishlistID || null } })
        onSave()
      })} />}
  </Modal>
}

function SelectWishlist({ initial, busy, onSave }: {
  initial?: string; busy: boolean; onSave: (id: string) => void
}) {
  const page = useResource(loadOwnedLists)
  const choices = page.value?.filter((list) => list.phase === 'editing' || list.phase === 'holiday')
  const available = choices?.some((list) => list.id === initial)
  return <>
    <Alert message={page.error} />
    {choices ? <form className="form" onSubmit={(event) => {
      event.preventDefault()
      onSave(String(new FormData(event.currentTarget).get('list')))
    }}>
      <label>Ваши списки<select name="list" defaultValue={available ? initial : ''}>
        <option value="">Без списка</option>
        {choices.map((list) => <option key={list.id} value={list.id}>{list.title} · {formatDate(list.holiday_date)}</option>)}
      </select></label>
      <button disabled={busy}>Сохранить</button>
    </form> : <PageStatus {...page} />}
  </>
}

interface RecipientList extends Details {
  phase: Phase
  close_at: string | null
  avatar_url: string | null
  gifts: Gift[]
}

function RecipientWishlist({ path, refreshEvent }: { path: string; refreshEvent: () => void }) {
  const [sort, setSort] = useState<GiftSort>('priority')
  const page = usePagedResource<RecipientList>(path + '?sort=' + sort)
  const action = useAction(page.refresh)
  const { value: list, error } = page

  if (!list || error) return <section className="stack">
    <Alert message={error} />
    {error ? <button className="secondary" onClick={() => { refreshEvent(); page.refresh() }}>Обновить событие</button> : <PageStatus {...page} />}
  </section>

  const canBook = list.phase === 'editing' || list.phase === 'holiday'
  return <section className="stack">
    <header className="row spread">
      <div className="row wishlist-title">
        {list.avatar_url && <Avatar name={list.title} url={list.avatar_url} />}
        <div className="page-head">
          <h2>{list.title}</h2>
          <p className="muted">{formatDate(list.holiday_date)}</p>
        </div>
      </div>
      <GiftSorting value={sort} onChange={(next) => { page.first(); setSort(next) }} />
    </header>
    <Alert message={action.error} />
    {list.gifts.length ? <ul className="card-grid gift-grid">
      {list.gifts.map((gift) => {
        const reservation = path + '/gifts/' + gift.id + '/reservation'
        return <GiftCard key={gift.id} gift={gift} view="shared" booking={canBook ? {
          busy: action.busy,
          reserve: () => void action.run(() => request(reservation, { method: 'POST' })),
          cancel: () => void action.run(() => request(reservation, { method: 'DELETE' })),
        } : undefined} />
      })}
    </ul> : <p className="panel muted empty">В этом списке пока нет видимых подарков.</p>}
    <PageButtons {...page} disabled={action.busy} />
    {canBook && list.close_at && <p className="muted">Бронирование по правилам списка до {formatInstant(list.close_at, list.time_zone)}.</p>}
  </section>
}

interface DrawRequest {
  version: string
  confirm_round?: number
}

function EventAdmin({ event, userID, initialToken, refresh }: {
  event: EventView; userID: string; initialToken?: string; refresh: () => void
}) {
  const navigate = useNavigate()
  const [copyStatus, setCopyStatus] = useState('')
  const [removal, setRemoval] = useState<Application>()
  const [warning, setWarning] = useState<{ input: DrawRequest; text: string }>()
  const action = useAction(refresh)
  const path = eventPath(event.id)
  const applications = event.applications ?? []
  const participantCount = applications.filter((applicant) => applicant.status === 'approved').length
  const link = initialToken && window.location.origin + invitePath(initialToken)

  async function copyLink() {
    if (!link) return
    try {
      await navigator.clipboard.writeText(link)
      setCopyStatus('Ссылка скопирована')
    } catch {
      setCopyStatus('Выделите и скопируйте ссылку из поля')
    }
  }

  function decide(applicant: Application, status: ApplicationStatus) {
    void action.run(async () => {
      await request(path + '/applications/' + applicant.user_id, {
        method: 'PUT', body: { version: event.version, status },
      })
      setRemoval(undefined)
    })
  }

  function draw(input: DrawRequest) {
    void action.run(async () => {
      try {
        await request(path + '/draw', { method: 'POST', body: input })
        setWarning(undefined)
      } catch (error) {
        if (error instanceof APIError && error.status === 409 && error.confirmRound !== undefined) {
          setWarning({ input: { ...input, confirm_round: error.confirmRound }, text: error.message })
          return
        }
        setWarning(undefined)
        throw error
      }
    })
  }

  return <section className="panel santa-panel">
    <h2>Управление событием</h2>
    <Alert message={action.error} />
    {!event.closed && <div className="santa-panel-content">
      {link ? <label>Общая ссылка для приглашения<input className="copy-link" readOnly value={link} title="Нажмите, чтобы скопировать ссылку"
        onFocus={(event) => event.currentTarget.select()}
        onClick={(event) => { event.currentTarget.select(); void copyLink() }}
        onKeyDown={(event) => {
          if (event.key === 'Enter' || event.key === ' ') {
            event.preventDefault()
            void copyLink()
          }
        }} /></label>
        : <p className="muted">Выпустите новую ссылку, если вам нужно снова поделиться приглашением.</p>}
      <div className="row">
        {link && <button className="secondary" disabled={action.busy} onClick={() => void copyLink()}>Скопировать ссылку</button>}
        <button className="secondary" disabled={action.busy} onClick={() => void action.run(async () => {
          const invite = await request<InvitationLink>(path + '/invitation', { method: 'POST' })
          navigate(path, { replace: true, state: { inviteToken: invite.invite_token } })
          setCopyStatus('')
        })}>Перевыпустить ссылку</button>
      </div>
      <p className="muted" role="status">{copyStatus}</p>
      <p className="muted">После перевыпуска прежняя ссылка перестанет действовать.</p>
    </div>}
    <h3>Заявки и состав</h3>
    {applications.length ? <ul className="santa-applications">
      {applications.map((applicant) => <li key={applicant.user_id} className="row spread">
        <div className="page-head">
          <strong>{applicant.name}</strong>
          <span className="muted">{applicationLabels[applicant.status]}</span>
        </div>
        {!event.closed && <div className="row">
          {applicant.status !== 'approved' && <button className="secondary" disabled={action.busy}
            onClick={() => decide(applicant, 'approved')}>Одобрить</button>}
          {applicant.status === 'pending' && <button className="secondary" disabled={action.busy}
            onClick={() => decide(applicant, 'rejected')}>Отклонить</button>}
          {applicant.status === 'approved' && applicant.user_id !== userID && <button className="secondary" disabled={action.busy}
            onClick={() => setRemoval(applicant)}>Исключить</button>}
        </div>}
      </li>)}
    </ul> : <p className="muted">Заявок пока нет.</p>}
    {!event.closed && <div className="santa-panel-content">
      <p className="muted">В жеребьёвке участвуют все одобренные участники.</p>
      {participantCount < 2 && <p className="muted">Для жеребьёвки нужны минимум два участника.</p>}
      {(participantCount === 2 || participantCount === 3) && <p className="muted">
        В группе из двух или трёх человек участники могут вычислить дарителя по своему назначению.
      </p>}
      <div><button disabled={action.busy || participantCount < 2} onClick={() => draw({ version: event.version })}>
        {event.round > 0 ? 'Пережеребить' : 'Провести жеребьёвку'}
      </button></div>
    </div>}
    {!event.closed && removal && <Modal title="Исключить участника?" onClose={() => setRemoval(undefined)} busy={action.busy}>
      <Alert message={action.error} />
      <p><strong>{removal.name}</strong> потеряет доступ к своему назначению. Существующие брони останутся в разделе «Брони и история».</p>
      <div className="row">
        <button className="secondary" disabled={action.busy} onClick={() => setRemoval(undefined)}>Отмена</button>
        <button disabled={action.busy} onClick={() => decide(removal, 'removed')}>Исключить участника</button>
      </div>
    </Modal>}
    {!event.closed && warning && <Modal title="Пережеребьёвка" onClose={() => setWarning(undefined)} busy={action.busy}>
      <p>{warning.text}</p>
      <div className="row">
        <button className="secondary" disabled={action.busy} onClick={() => setWarning(undefined)}>Вернуться</button>
        <button disabled={action.busy} onClick={() => draw(warning.input)}>Пережеребить</button>
      </div>
    </Modal>}
  </section>
}

