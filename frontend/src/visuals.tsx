import { useState } from 'react'

const icons = {
  gift: <><rect x="3" y="8" width="18" height="4" rx="1" /><path d="M5 12v8h14v-8M12 8v12M12 8H8.5A2.5 2.5 0 1 1 11 5.5L12 8Zm0 0h3.5A2.5 2.5 0 1 0 13 5.5L12 8Z" /></>,
  calendar: <><rect x="3" y="5" width="18" height="16" rx="2" /><path d="M7 3v4m10-4v4M3 10h18" /></>,
  user: <><circle cx="12" cy="7" r="4" /><path d="M3 22v-2a5 5 0 0 1 5-5h8a5 5 0 0 1 5 5v2Z" /></>,
  people: <><circle cx="9" cy="8" r="3" /><path d="M3 20v-2a6 6 0 0 1 12 0v2m1-15a3 3 0 0 1 0 6m2 2a5 5 0 0 1 3 5v2" /></>,
  plus: <path d="M12 5v14M5 12h14" />,
  back: <path d="m11 5-7 7 7 7M4 12h16" />,
  forward: <path d="m13 5 7 7-7 7M4 12h16" />,
  snowflake: <path d="M12 2v20M4 7l16 10M4 17 20 7M9 4l3 3 3-3m-6 16 3-3 3 3M4 11l4-1-1-4m10 12-1-4 4-1M4 13l4 1-1 4m10-12-1 4 4 1" />,
  share: <path d="M7 10H5a1 1 0 0 0-1 1v9a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1v-9a1 1 0 0 0-1-1h-2M12 15V3M7 8l5-5 5 5" />,
  settings: <><circle cx="12" cy="12" r="3" /><path d="m10 3-.5 2.4-1.2.7L6 5.3 4 7.6l1.3 2.2-.2 1.3L3 12.5l.7 3 2.5.2.9 1-.1 2.5 2.8 1.3 1.7-1.8h1.3l1.7 1.8 2.8-1.3-.1-2.5.9-1 2.5-.2.7-3-2.1-1.4-.2-1.3L20 7.6l-2-2.3-2.3.8-1.2-.7L14 3Z" /></>,
  more: <path d="M5 12h.01M12 12h.01M19 12h.01" strokeWidth="3.5" />,
  close: <path d="m6 6 12 12M6 18 18 6" />,
  check: <path d="m5 12 4 4L19 6" strokeWidth="2.5" />,
  down: <path d="m6 9 6 6 6-6" />,
}

export function Icon({ name }: { name: keyof typeof icons }) {
  return <svg className="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75"
    strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">{icons[name]}</svg>
}

type IllustrationName = 'gift-gold' | 'gift-violet' | 'gift-occasion' | 'ornament' | 'house' | 'headphones' | 'mug'
  | 'game' | 'game-shared' | 'game-archive' | 'game-booking' | 'tea' | 'book'

export function Illustration({ name, eager = false }: { name: IllustrationName; eager?: boolean }) {
  return <img className={`illustration art-${name}`} src={`/illustrations/${name}.webp`}
    alt="" loading={eager ? 'eager' : 'lazy'} decoding="async" />
}

export function OccasionArt({ title, url }: { title: string; url?: string | null }) {
  const [failed, setFailed] = useState('')
  if (url && url !== failed) return <img className="list-cover" src={url} alt="" loading="lazy" onError={() => setFailed(url)} />
  const name = /новый год|новогод|рождеств|christmas|new year/iu.test(title) ? 'ornament'
    : /новосель|housewarming/iu.test(title) ? 'house' : 'gift-occasion'
  return <Illustration name={name} />
}

const giftIllustrations = [
  [/наушник|\bheadphones\b/iu, 'headphones'],
  [/кружк|\bmug\b/iu, 'mug'],
  [/настольн.*игр|board game/iu, 'game'],
  [/чайный набор|чайник|tea set/iu, 'tea'],
  [/книга|книги|книгу|\bbook\b/iu, 'book'],
] as const

export function GiftArt({ title, view = 'owner' }: { title: string; view?: 'owner' | 'shared' | 'archive' | 'booking' }) {
  const name = giftIllustrations.find(([pattern]) => pattern.test(title))?.[1] ?? 'gift-violet'
  return <Illustration name={name === 'game' && view !== 'owner' ? `game-${view}` : name} />
}

export function Avatar({ name, url }: { name: string; url?: string | null }) {
  const [failed, setFailed] = useState('')
  return <span className="avatar" aria-hidden="true">
    {url && url !== failed ? <img src={url} alt="" onError={() => setFailed(url)} /> : Array.from(name.trim())[0]?.toLocaleUpperCase('ru')}
  </span>
}
