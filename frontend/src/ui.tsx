import { useCallback, useEffect, useId, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import type { Card, Details, Gift, GiftSort, Phase, Priority } from './api'
import { APIError, errorMessage, requestPage } from './api'
import { DateField } from './DateField'
import { Icon } from './visuals'

export const phaseLabels: Record<Phase, string> = {
  editing: 'Активный',
  holiday: 'День праздника',
  awaiting_archive: 'Ожидает архивации',
  archived: 'В архиве',
}

const calendarDate = new Intl.DateTimeFormat('ru', {
  day: 'numeric', month: 'long', year: 'numeric', timeZone: 'UTC',
})

export function formatDate(date?: string | null) {
  if (!date) return 'Без даты'
  return calendarDate.format(new Date(date + 'T00:00:00Z')).replace(/\sг\.$/u, '')
}

export function formatInstant(instant: string, timeZone?: string | null) {
  const options = { dateStyle: 'medium', timeStyle: 'short' } as const
  const date = new Date(instant)
  try {
    return new Intl.DateTimeFormat('ru', { ...options, timeZone: timeZone ?? undefined }).format(date)
  } catch (error) {
    if (!(error instanceof RangeError) || !timeZone) throw error
    return new Intl.DateTimeFormat('ru', { ...options, timeZone: 'UTC' }).format(date) + ' UTC'
  }
}

export function Alert({ message }: { message: string }) {
  return message ? <p className="alert" role="alert">{message}</p> : null
}

export function Modal({ title, children, busy, onClose }: {
  title: string
  children: ReactNode
  busy?: boolean
  onClose: () => void
}) {
  const ref = useRef<HTMLDialogElement>(null)
  const heading = useId()
  useEffect(() => {
    const dialog = ref.current!
    dialog.showModal()
    return () => dialog.close()
  }, [])
  return <dialog ref={ref} className="panel modal" aria-labelledby={heading} onCancel={(event) => {
    event.preventDefault()
    if (!busy) onClose()
  }}>
    <header className="row spread">
      <h2 id={heading}>{title}</h2>
      <button type="button" className="secondary icon-button" aria-label="Закрыть" disabled={busy} onClick={onClose}>
        <Icon name="close" />
      </button>
    </header>
    {children}
  </dialog>
}

export function useResource<T>(load: (signal: AbortSignal) => Promise<T>) {
  const [value, setValue] = useState<T>()
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)
  useEffect(() => {
    const controller = new AbortController()
    load(controller.signal).then(
      (data) => {
        if (controller.signal.aborted) return
        setValue(data)
        setError('')
      },
      (error: unknown) => {
        if (!controller.signal.aborted) setError(errorMessage(error))
      },
    )
    return () => controller.abort()
  }, [load, revision])
  const refresh = useCallback(() => setRevision((n) => n + 1), [])
  return { value, error, refresh }
}

export function usePagedResource<T>(path: string) {
  const [cursors, setCursors] = useState([''])
  const cursor = cursors[cursors.length - 1]
  const load = useCallback(async (signal: AbortSignal) => ({
    cursor, path,
    ...await requestPage<T>(path, cursor, signal),
  }), [path, cursor])
  const { value, error, refresh } = useResource(load)
  const page = value?.path === path && value.cursor === cursor ? value : undefined
  const next = page?.next
  return {
    value: page?.data,
    error,
    refresh,
    first: () => setCursors(['']),
    previous: cursors.length > 1 ? () => setCursors(cursors.slice(0, -1)) : undefined,
    next: next ? () => setCursors([...cursors, next]) : undefined,
  }
}

export function PageButtons({ previous, next, disabled }: {
  previous?: () => void
  next?: () => void
  disabled?: boolean
}) {
  if (!previous && !next) return null
  return <div className="row">
    {previous && <button className="secondary" disabled={disabled} onClick={previous}>Предыдущая страница</button>}
    {next && <button className="secondary" disabled={disabled} onClick={next}>Следующая страница</button>}
  </div>
}

export function PageStatus({ error, refresh }: { error: string; refresh: () => void }) {
  return error
    ? <button className="secondary" onClick={refresh}>Повторить</button>
    : <p role="status">Загрузка…</p>
}

export function useAction(refresh?: () => void) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  return {
    busy, error,
    run: async (action: () => Promise<unknown>) => {
      if (busy) return
      setBusy(true)
      setError('')
      try {
        await action()
        refresh?.()
      } catch (error) {
        setError(errorMessage(error))
        if (error instanceof APIError && (error.status === 409 || error.status === 404)) refresh?.()
      } finally {
        setBusy(false)
      }
    },
  }
}

const localZone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
const zones = Intl.supportedValuesOf('timeZone')

function lastHolidayDate(timeZone: string) {
  try {
    const parts = new Intl.DateTimeFormat('en', {
      timeZone, year: 'numeric', month: '2-digit', day: '2-digit',
    }).formatToParts()
    const year = Number(parts.find((part) => part.type === 'year')!.value) + 1
    const month = parts.find((part) => part.type === 'month')!.value
    const day = parts.find((part) => part.type === 'day')!.value
    return `${year}-${month}-${month === '02' && day === '29' ? '28' : day}`
  } catch (error) {
    if (!(error instanceof RangeError)) throw error
    return undefined
  }
}

export function DetailsForm({ initial, suggestedDate, submit, busy, label }: {
  initial?: Partial<Details>
  suggestedDate?: string
  submit: (details: Details) => void
  busy: boolean
  label: string
}) {
  const [dated, setDated] = useState(Boolean(initial?.holiday_date))
  const [zone, setZone] = useState(initial?.time_zone ?? localZone)

  return <form className="form" onSubmit={(event) => {
    event.preventDefault()
    const data = new FormData(event.currentTarget)
    submit({ title: String(data.get('title')), holiday_date: dated ? String(data.get('date')) : null, time_zone: dated ? zone : null })
  }}>
    <label>Название списка<input name="title" required maxLength={150} defaultValue={initial?.title} placeholder="День рождения" /></label>
    <div className="row filters" role="group" aria-label="Дата праздника">
      <button type="button" aria-pressed={!dated} onClick={() => setDated(false)}>Без даты</button>
      <button type="button" aria-pressed={dated} onClick={() => setDated(true)}>С датой</button>
    </div>
    {dated && <div className="form-row">
      <DateField label="Дата праздника" name="date" required defaultValue={initial?.holiday_date ?? suggestedDate}
        max={lastHolidayDate(zone.trim())} rangeError="Выберите дату праздника не дальше чем на год вперёд." />
      <label>Часовой пояс IANA<input name="zone" list="time-zones" required value={zone} onChange={(event) => setZone(event.currentTarget.value)} /></label>
    </div>}
    <button disabled={busy}>{label}</button>
  </form>
}

export function TimeZones() {
  return <datalist id="time-zones">
    <option value="UTC" />
    {zones.map((zone) => <option key={zone} value={zone} />)}
  </datalist>
}

const priorityLabels: Record<Priority, string> = { high: 'Высокий', normal: 'Обычный', low: 'Низкий' }

export function GiftSorting({ value, onChange }: { value: GiftSort; onChange: (sort: GiftSort) => void }) {
  return <label className="gift-sorting"><span className="sr-only">Сортировка подарков</span>
    <select value={value} onChange={(event) => onChange(event.currentTarget.value as GiftSort)}>
      <option value="priority">По приоритету</option>
      <option value="name">По имени</option>
      <option value="added">По дате добавления</option>
    </select>
  </label>
}

export const formatGiftCount = (count: number) => {
  const n = count % 100
  const last = n % 10
  const word = n >= 11 && n <= 14 ? 'подарков' : last === 1 ? 'подарок' : last >= 2 && last <= 4 ? 'подарка' : 'подарков'
  return `${count} ${word}`
}

export function CardForm({ initial, submit, busy, uploadsEnabled }: {
  initial?: Gift
  submit: (card: Card | FormData) => void
  busy: boolean
  uploadsEnabled?: boolean
}) {
  const [photoError, setPhotoError] = useState('')
  const photos = initial?.photos ?? []
  return <form className="form" onSubmit={(event) => {
    event.preventDefault()
    const data = new FormData(event.currentTarget)
    const card: Card = {
      title: String(data.get('title')),
      description: String(data.get('description')).trim() || null,
      product_url: String(data.get('url')).trim() || null,
      price: String(data.get('price')).trim().replace(',', '.') || null,
      priority: data.get('priority') as Priority,
    }
    const added = data.getAll('photo').filter((entry): entry is File => entry instanceof File && entry.name !== '')
    const removed = data.getAll('remove_photo')
    if (photos.length - removed.length + added.length > 5) {
      setPhotoError('На карточке может быть не больше 5 фотографий.')
      return
    }
    if (added.some((file) => file.size > 5 * 1024 * 1024)) {
      setPhotoError('Максимальный размер одной фотографии — 5 МБ.')
      return
    }
    setPhotoError('')
    if (added.length || removed.length) {
      const form = new FormData()
      form.set('card', JSON.stringify(card))
      added.forEach((file) => form.append('photo', file))
      removed.forEach((id) => form.append('remove_photo', id))
      submit(form)
    } else {
      submit(card)
    }
  }}>
    <label>Название подарка<input name="title" required maxLength={150} defaultValue={initial?.title} placeholder="Книга, настольная игра…" /></label>
    <label>Описание<textarea name="description" rows={3} maxLength={4000} defaultValue={initial?.description ?? ''} /></label>
    <label>Ссылка на товар<input name="url" type="url" maxLength={4096} defaultValue={initial?.product_url ?? ''} placeholder="https://…" /></label>
    <div className="form-row">
      <label>Цена, ₽<input name="price" inputMode="decimal" pattern="[0-9]{1,10}([.,][0-9]{1,2})?" placeholder="Например, 1490,50"
        defaultValue={initial?.price?.replace('.', ',') ?? ''} /></label>
      <label>Приоритет<select name="priority" defaultValue={initial?.priority ?? 'normal'}>
        {Object.entries(priorityLabels).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
      </select></label>
    </div>
    {(uploadsEnabled || photos.length > 0) && <fieldset className="photo-fields" disabled={busy} onChange={() => setPhotoError('')}>
      <legend>Фотографии</legend>
      {photos.length > 0 && <div className="photo-previews">
        {photos.map((photo, index) => <label key={photo.id} className="photo-preview">
          <img src={photo.url} alt={`Фото ${index + 1}`} />
          <span><input type="checkbox" name="remove_photo" value={photo.id} aria-label={`Удалить фото ${index + 1}`} />Удалить</span>
        </label>)}
      </div>}
      {uploadsEnabled && <label>
        Добавить фотографии
        <input type="file" name="photo" accept="image/jpeg,image/png,image/webp" multiple aria-describedby="photo-limits" />
        <small className="muted" id="photo-limits">До 5 фото в карточке. JPEG, PNG или WebP до 5 МБ каждое.</small>
      </label>}
      <Alert message={photoError} />
    </fieldset>}
    <button disabled={busy}>{initial ? 'Сохранить карточку' : 'Добавить подарок'}</button>
  </form>
}

export function AvatarFile({ label, busy, onUpload }: { label: string; busy: boolean; onUpload: (body: FormData) => void }) {
  return <label className="avatar-upload">
    {label}
    <input type="file" accept="image/jpeg,image/png,image/webp" disabled={busy} onChange={(event) => {
      const file = event.currentTarget.files?.[0]
      event.currentTarget.value = ''
      if (!file) return
      const body = new FormData()
      body.set('photo', file)
      onUpload(body)
    }} />
  </label>
}
