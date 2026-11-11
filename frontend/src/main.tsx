import { StrictMode, useEffect, useRef, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Link, NavLink, Route, Routes, useLocation, useParams } from 'react-router-dom'
import { APIError, errorMessage, ownerPath, request, sharedProfilePath, sharePath, userPath } from './api'
import type { Profile } from './api'
import { BookingsPage, ListsPage } from './Dashboard'
import { SantaEventPage, SantaEventsPage, SantaInvitationPage } from './SantaPage'
import { ConnectionsPage, SharedProfilePage } from './SharedProfilePage'
import { ProfilePage } from './ProfileSettings'
import { WishlistPage } from './WishlistPage'
import { Alert, TimeZones, useAction } from './ui'
import { Avatar, Icon, Illustration } from './visuals'
import './style.css'

function App() {
  const [profile, setProfile] = useState<Profile | null>()
  const [error, setError] = useState('')
  const [retry, setRetry] = useState(0)
  const logout = useAction()
  const location = useLocation()
  const viaShare = location.pathname.startsWith('/shared/')
  useEffect(() => {
    const controller = new AbortController()
    const onUnauthorized = () => setProfile(null)
    window.addEventListener('wishlist:unauthorized', onUnauthorized)
    request<Profile>('/me', { signal: controller.signal }).then(setProfile, (error: unknown) => {
      if (controller.signal.aborted) return
      if (error instanceof APIError && error.status === 401) setProfile(null)
      else setError(errorMessage(error))
    })
    return () => {
      controller.abort()
      window.removeEventListener('wishlist:unauthorized', onUnauthorized)
    }
  }, [retry])

  return <>
    <header className="site-header">
      <Link className="wordmark" to="/"><span className="wordmark-icon"><Icon name="gift" /></span>Вишлист</Link>
      {profile && <AccountMenu profile={profile} busy={logout.busy} onLogout={() => void logout.run(async () => {
        await request('/logout', { method: 'POST' })
        setProfile(null)
      })} />}
    </header>
    {profile ? <div className={`app-shell${viaShare ? ' shared-shell' : ''}`}>
      {!viaShare && <nav className="sidebar" aria-label="Основная навигация">
        <div className="sidebar-links">
          <NavLink to="/" end className={location.pathname.startsWith('/wishlists/') ? 'current' : undefined}>
            <Icon name="gift" /><span>Мои списки</span>
          </NavLink>
          <NavLink to="/bookings"><Icon name="calendar" /><span>Брони и история</span></NavLink>
          <NavLink to="/subscriptions" className={location.pathname.startsWith('/users/') ? 'current' : undefined}>
            <Icon name="people" /><span>Подписки</span>
          </NavLink>
        </div>
        <NavLink className="santa-shortcut" to="/santa"><Icon name="snowflake" /><span>Тайный Санта</span></NavLink>
      </nav>}
      <main className="app-main stack">
        <Alert message={logout.error} />
        <Routes>
          <Route path="/" element={<ListsPage />} />
          <Route path="/santa" element={<SantaEventsPage />} />
          <Route path="/santa/events/:id" element={<SantaEventRoute userID={profile.id} />} />
          <Route path="/santa/invitations/:token" element={<SantaInvitationRoute />} />
          <Route path="/bookings" element={<BookingsPage />} />
          <Route path="/subscriptions" element={<ConnectionsPage />} />
          <Route path="/users/:id" element={<SharedProfileRoute />} />
          <Route path="/profile" element={<ProfilePage profile={profile} onProfile={setProfile} />} />
          <Route path="/wishlists/:id" element={<ListRoute />} />
          <Route path="/shared/:token" element={<ListRoute />} />
          <Route path="/shared/:token/list" element={<ListRoute />} />
          <Route path="/shared/:token/profile" element={<SharedProfileRoute />} />
          <Route path="*" element={<><h1>Страница не найдена</h1><Link to="/">В личный кабинет</Link></>} />
        </Routes>
        <TimeZones />
      </main>
    </div> : <main className="guest-main">
      <Alert message={error} />
      {error && <button className="secondary" onClick={() => { setError(''); setRetry((n) => n + 1) }}>Повторить</button>}
      {profile === undefined && !error && <p role="status">Проверяем сессию…</p>}
      {profile === null && <SignIn onProfile={setProfile} />}
    </main>}
  </>
}

function AccountMenu({ profile, busy, onLogout }: { profile: Profile; busy: boolean; onLogout: () => void }) {
  const menu = useRef<HTMLDetailsElement>(null)
  const toggle = useRef<HTMLElement>(null)
  useEffect(() => {
    const closeOutside = (event: PointerEvent) => {
      if (menu.current?.open && !menu.current.contains(event.target as Node)) menu.current.open = false
    }
    document.addEventListener('pointerdown', closeOutside)
    return () => document.removeEventListener('pointerdown', closeOutside)
  }, [])

  return <details className="account-menu" ref={menu} onKeyDown={(event) => {
    if (event.key === 'Escape' && event.currentTarget.open) {
      event.currentTarget.open = false
      toggle.current?.focus()
    }
  }}>
    <summary ref={toggle} aria-label="Меню профиля">
      <Avatar name={profile.display_name} url={profile.avatar_url} />
      <Icon name="down" />
    </summary>
    <div className="panel account-popover">
      <Link className="account-profile" to="/profile" onClick={() => { if (menu.current) menu.current.open = false }}>
        <Avatar name={profile.display_name} url={profile.avatar_url} />
        <span className="page-head">
          <strong>{profile.display_name}</strong>
          <span className="muted">{profile.email}</span>
          <span className="account-profile-link">Профиль →</span>
        </span>
      </Link>
      <button className="secondary" disabled={busy} onClick={onLogout}>Выйти</button>
    </div>
  </details>
}

function SantaEventRoute({ userID }: { userID: string }) {
  const { id = '' } = useParams()
  return <SantaEventPage key={id} id={id} userID={userID} />
}

function SantaInvitationRoute() {
  const { token = '' } = useParams()
  return <SantaInvitationPage key={token} token={token} />
}

function ListRoute() {
  const { id, token } = useParams()
  const path = token ? sharePath(token) : ownerPath(id ?? '')
  return <WishlistPage key={path} path={path} />
}

function SharedProfileRoute() {
  const { id, token } = useParams()
  const path = token ? sharedProfilePath(token) : userPath(id ?? '')
  return <SharedProfilePage key={path} path={path} />
}

function SignIn({ onProfile }: { onProfile: (profile: Profile) => void }) {
  const [register, setRegister] = useState(false)
  const action = useAction()
  const location = useLocation()
  return <div className="auth-layout">
    <section className="auth-hero">
      <h1>Подарки,<br />которые ждут</h1>
      <p>Соберите список желаний и поделитесь им с друзьями.</p>
      <Illustration name="gift-gold" eager />
    </section>
    <section className="panel auth">
      <h2>{register ? 'Регистрация' : 'Войти'}</h2>
      <div className="auth-switch" role="group" aria-label="Доступ к аккаунту">
        <button type="button" aria-pressed={!register} disabled={action.busy} onClick={() => setRegister(false)}>Вход</button>
        <button type="button" aria-pressed={register} disabled={action.busy} onClick={() => setRegister(true)}>Регистрация</button>
      </div>
      {location.pathname.startsWith('/santa/invitations/') && <p className="muted">Войдите или зарегистрируйтесь, чтобы открыть приглашение в «Тайного Санту».</p>}
      {location.pathname.startsWith('/shared/') && <p className="muted">Войдите или зарегистрируйтесь, чтобы открыть ссылку друга.</p>}
      <Alert message={action.error} />
      <form className="form" onSubmit={(event) => {
        event.preventDefault()
        const data = new FormData(event.currentTarget)
        void action.run(async () => {
          const body = {
            email: String(data.get('email')),
            password: String(data.get('password')),
            ...(register ? { display_name: String(data.get('display_name')) } : {}),
          }
          await request(register ? '/register' : '/login', { method: 'POST', body })
          onProfile(await request<Profile>('/me'))
        })
      }}>
        {register && <label><span className="sr-only">Имя</span><input name="display_name" placeholder="Имя" autoComplete="name" required maxLength={100} /></label>}
        <label><span className="sr-only">Email</span><input type="email" name="email" placeholder="Email" autoComplete="email" required maxLength={254} /></label>
        <label><span className="sr-only">Пароль</span><input type="password" name="password" placeholder="Пароль" required minLength={register ? 8 : undefined}
          autoComplete={register ? 'new-password' : 'current-password'} /></label>
        <button disabled={action.busy}>{register ? 'Зарегистрироваться' : 'Войти'}</button>
      </form>
    </section>
  </div>
}

createRoot(document.getElementById('root')!).render(
  <StrictMode><BrowserRouter><App /></BrowserRouter></StrictMode>,
)
