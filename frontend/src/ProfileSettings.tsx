import { useState } from 'react'
import { Link } from 'react-router-dom'
import { request, userPath } from './api'
import type { Address, Profile } from './api'
import { DateField } from './DateField'
import { Alert, AvatarFile, Modal, PageStatus, useAction, useResource } from './ui'
import { Avatar, Icon } from './visuals'

export function ProfilePage({ profile, onProfile }: { profile: Profile; onProfile: (profile: Profile) => void }) {
  const action = useAction()
  const [saved, setSaved] = useState(false)
  const [shoes, setShoes] = useState(profile.shoe_size_eu?.replace(/\.0$/, '') ?? '')
  // Align the native step with the entered size so values like 42.3 stay valid.
  const shoeMin = (Math.abs(Math.round(Number(shoes) * 10) % 5) || 5) / 10

  return <section className="profile-page stack">
    <h1>Профиль</h1>
    <div className="panel profile-panel">
      <header className="profile-identity">
        <Avatar name={profile.display_name} url={profile.avatar_url} />
        <div className="page-head">
          <h2>{profile.display_name}</h2>
          <p className="muted">{profile.email}</p>
          <Link to={userPath(profile.id)}>Открыть общую часть профиля</Link>
        </div>
      </header>
      <AvatarSettings profile={profile} onProfile={onProfile} />
      <Alert message={action.error} />
      <form className="form" onChange={() => setSaved(false)} onSubmit={(event) => {
        event.preventDefault()
        const data = new FormData(event.currentTarget)
        setSaved(false)
        void action.run(async () => {
          const next = await request<Profile>('/me', {
            method: 'PUT',
            body: {
              clothing_size: String(data.get('clothing')).trim() || null,
              shoe_size_eu: String(data.get('shoes')).trim() || null,
              birth_date: String(data.get('birthday')) || null,
              description: String(data.get('description')).trim() || null,
            },
          })
          onProfile(next)
          setSaved(true)
        })
      }}>
        <label>О себе<textarea name="description" rows={4} maxLength={1000} defaultValue={profile.description ?? ''} />
          <small className="muted">Описание видно всем вошедшим пользователям, которые откроют ваш профиль.</small></label>
        <label>Размер одежды<input name="clothing" maxLength={40} defaultValue={profile.clothing_size ?? ''} placeholder="Например, M" /></label>
        <label>Размер обуви (EU)<input name="shoes" type="number" min={shoeMin} max={999.9} step={0.5}
          value={shoes} onChange={(event) => setShoes(event.currentTarget.value)} /></label>
        <DateField label="Дата рождения" name="birthday" defaultValue={profile.birth_date ?? ''}
          max={new Date().toISOString().slice(0, 10)} rangeError="Дата рождения не может быть в будущем." />
        <p className="muted profile-note">В общей части профиля видны размеры и возраст в полных годах.</p>
        <div className="row profile-actions">
          <button disabled={action.busy}>Сохранить</button>
          <span role="status" className="muted">{saved && 'Профиль сохранён'}</span>
        </div>
      </form>
    </div>
    <AddressSettings />
  </section>
}

function AvatarSettings({ profile, onProfile }: { profile: Profile; onProfile: (profile: Profile) => void }) {
  const action = useAction()
  return <section className="stack">
    <Alert message={action.error} />
    <div className="row">
      {profile.avatar_uploads_enabled && <AvatarFile label={profile.avatar_url ? 'Заменить аватарку' : 'Загрузить аватарку'}
        busy={action.busy} onUpload={(body) => void action.run(async () =>
          onProfile(await request<Profile>('/me/avatar', { method: 'PUT', body })))} />}
      {profile.avatar_url && <button className="secondary" disabled={action.busy} onClick={() =>
        void action.run(async () => onProfile(await request<Profile>('/me/avatar', { method: 'DELETE' })))}>
        Удалить аватарку
      </button>}
    </div>
  </section>
}

const addressesPath = '/me/addresses'
const loadAddresses = (signal: AbortSignal) => request<Address[]>(addressesPath, { signal })

function AddressSettings() {
  const page = useResource(loadAddresses)
  const action = useAction(page.refresh)
  const [editing, setEditing] = useState<Address | 'new'>()
  return <section className="panel profile-panel">
    <header className="row spread">
      <h2>Адреса доставки</h2>
      <button className="secondary" disabled={!page.value || page.value.length >= 10 || action.busy}
        onClick={() => setEditing('new')}><Icon name="plus" />Добавить адрес</button>
    </header>
    <p className="muted">Адреса видны вам и пользователям, с которыми вы подписаны друг на друга. Можно сохранить до 10 адресов.</p>
    <Alert message={page.error || action.error} />
    {!page.value ? <PageStatus {...page} /> : page.value.length ? <ul className="addresses">
      {page.value.map((entry) => <li className="row spread" key={entry.id}>
        <div className="page-head"><strong>{entry.label}</strong><p className="description">{entry.address}</p></div>
        <div className="row">
          <button className="secondary" disabled={action.busy} onClick={() => setEditing(entry)}>Изменить</button>
          <button className="secondary" disabled={action.busy} onClick={() => void action.run(() =>
            request(addressesPath + '/' + entry.id, { method: 'DELETE' }))}>Удалить</button>
        </div>
      </li>)}
    </ul> : <p className="muted">Адресов пока нет.</p>}
    {editing && <AddressDialog address={editing === 'new' ? undefined : editing}
      onClose={() => setEditing(undefined)} onSave={() => { setEditing(undefined); page.refresh() }} />}
  </section>
}

function AddressDialog({ address, onClose, onSave }: { address?: Address; onClose: () => void; onSave: () => void }) {
  const action = useAction()
  return <Modal title={address ? 'Изменить адрес' : 'Добавить адрес'} onClose={onClose} busy={action.busy}>
    <Alert message={action.error} />
    <form className="form" onSubmit={(event) => {
      event.preventDefault()
      const data = new FormData(event.currentTarget)
      void action.run(async () => {
        await request(address ? addressesPath + '/' + address.id : addressesPath, {
          method: address ? 'PUT' : 'POST',
          body: { label: String(data.get('label')), address: String(data.get('address')) },
        })
        onSave()
      })
    }}>
      <label>Название<input name="label" required maxLength={80} defaultValue={address?.label} placeholder="Например, Озон" /></label>
      <label>Адрес<textarea name="address" required rows={3} maxLength={500} defaultValue={address?.address} /></label>
      <button disabled={action.busy}>Сохранить</button>
    </form>
  </Modal>
}
