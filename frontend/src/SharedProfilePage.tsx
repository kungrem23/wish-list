import { Link, useSearchParams } from 'react-router-dom'
import { ownerPath, request, sharePath, userPath } from './api'
import type { FriendProfile, Person, Summary } from './api'
import { Alert, formatDate, formatGiftCount, PageButtons, PageStatus, useAction, usePagedResource } from './ui'
import { Avatar, Icon } from './visuals'

const years = new Intl.NumberFormat('ru', { style: 'unit', unit: 'year', unitDisplay: 'long' })

export function SharedProfilePage({ path }: { path: string }) {
  const page = usePagedResource<FriendProfile>(path)
  const action = useAction(page.refresh)
  const { value: profile, error } = page
  if (!profile || error) return <>
    <Alert message={error} />
    <PageStatus {...page} />
    <PageButtons {...page} />
  </>

  const facts = [
    ['Возраст', profile.age === null ? null : years.format(profile.age)],
    ['Размер одежды', profile.clothing_size],
    ['Размер обуви (EU)', profile.shoe_size_eu?.replace(/\.0$/, '').replace('.', ',')],
  ]

  return <div className="profile-page stack">
    <header className="panel profile-panel">
      <div className="row spread">
        <div className="profile-identity">
          <Avatar name={profile.display_name} url={profile.avatar_url} />
          <div className="page-head">
            <p className="muted">Профиль</p>
            <h1>{profile.display_name}</h1>
          </div>
        </div>
        {profile.is_self ? <Link to="/profile">Настройки профиля</Link> :
          <button className={profile.is_following ? 'secondary' : undefined} disabled={action.busy} onClick={() =>
            void action.run(async () => {
              await request(userPath(profile.id) + '/follow', { method: profile.is_following ? 'DELETE' : 'PUT' })
              page.first()
            })}>{profile.is_following ? 'Отписаться' : 'Подписаться'}</button>}
      </div>
      <Alert message={action.error} />
      {profile.description && <p className="description">{profile.description}</p>}
      {facts.some(([, value]) => value) && <dl className="friend-facts">
        {facts.map(([label, value]) => value && <div key={label}>
          <dt className="muted">{label}</dt>
          <dd>{value}</dd>
        </div>)}
      </dl>}
    </header>

    {Boolean(profile.addresses?.length) && <section className="panel profile-panel">
      <h2>Адреса доставки</h2>
      <dl className="addresses">
        {profile.addresses?.map((entry) => <div key={entry.id}>
          <dt>{entry.label}</dt><dd className="description">{entry.address}</dd>
        </div>)}
      </dl>
    </section>}

    <section className="stack" aria-labelledby="active-lists-heading">
      <h2 id="active-lists-heading">Списки желаний</h2>
      {profile.active_lists.length ? <ul className="profile-list-grid">
        {profile.active_lists.map((list) => <li key={list.id}><ProfileListLink list={list} own={profile.is_self} /></li>)}
      </ul> : <p className="panel muted empty">Активных списков пока нет.</p>}
      <PageButtons {...page} disabled={action.busy} />
    </section>
  </div>
}

function ProfileListLink({ list, own }: { list: Summary; own: boolean }) {
  return <Link className="panel profile-list-link" to={own ? ownerPath(list.id) : sharePath(list.share_token)}>
    {list.avatar_url ? <Avatar name={list.title} url={list.avatar_url} /> : <Icon name="gift" />}
    <div className="page-head">
      <h3>{list.title}</h3>
      <p className="muted">{formatGiftCount(list.gift_count)} · {formatDate(list.holiday_date)}</p>
    </div>
    <span className="profile-list-action">К подаркам <span aria-hidden="true">→</span></span>
  </Link>
}

export function ConnectionsPage() {
  const [params, setParams] = useSearchParams()
  const followers = params.get('tab') === 'followers'
  return <section className="profile-page stack">
    <h1>Подписки</h1>
    <div className="row filters page-tabs" role="group" aria-label="Списки подписок">
      <button aria-pressed={!followers} onClick={() => setParams({})}>Я подписан</button>
      <button aria-pressed={followers} onClick={() => setParams({ tab: 'followers' })}>Подписчики</button>
    </div>
    <ConnectionList key={String(followers)} followers={followers} />
  </section>
}

function ConnectionList({ followers }: { followers: boolean }) {
  const page = usePagedResource<Person[]>(followers ? '/me/followers' : '/me/following')
  return <>
    <Alert message={page.error} />
    {page.value === undefined ? <PageStatus {...page} /> : page.value.length ? <ul className="profile-list-grid">
      {page.value.map((person) => <li key={person.id}>
        <Link className="panel profile-list-link connection" to={userPath(person.id)}>
          <Avatar name={person.display_name} url={person.avatar_url} />
          <span>{person.display_name}</span>
          <span aria-hidden="true">→</span>
        </Link>
      </li>)}
    </ul> : <p className="panel muted empty">{followers ? 'У вас пока нет подписчиков.' : 'Вы пока ни на кого не подписаны.'}</p>}
    <PageButtons {...page} />
  </>
}
