export interface ProfileInput {
  clothing_size: string | null
  shoe_size_eu: string | null
  birth_date: string | null
  description: string | null
}

export interface Person {
  id: string
  display_name: string
  avatar_url: string | null
}

export interface Profile extends ProfileInput, Person {
  email: string
  avatar_uploads_enabled: boolean
}

export interface Address {
  id: string
  label: string
  address: string
}

export interface Details {
  title: string
  holiday_date?: string | null
  time_zone?: string | null
}

export type Phase = 'editing' | 'holiday' | 'awaiting_archive' | 'archived'

export interface Summary extends Details {
  id: string
  share_token: string
  close_at: string | null
  gift_count: number
  avatar_url: string | null
  archived_at: string | null
  phase: Phase
}

export type Priority = 'high' | 'normal' | 'low'
export type GiftSort = 'priority' | 'name' | 'added'

export interface Card {
  title: string
  description: string | null
  product_url: string | null
  price: string | null
  priority: Priority
}

export interface GiftPhoto {
  id: string
  url: string
}

export interface Gift extends Card {
  id: string
  active: boolean
  reservation?: 'mine' | 'taken' | 'free'
  gifted?: boolean
  photos?: GiftPhoto[]
}

export interface Wishlist extends Summary {
  is_owner: boolean
  owner_name: string
  gifts: Gift[]
  photo_uploads_enabled?: boolean
}

export interface FriendProfile extends Person {
  clothing_size: string | null
  shoe_size_eu: string | null
  age: number | null
  description: string | null
  is_following: boolean
  is_self: boolean
  addresses?: Address[]
  active_lists: Summary[]
}

interface GiftActivity {
  gift_id: string
  gift_title: string
  photos?: GiftPhoto[]
  wishlist_title: string
  recipient: string
  reserved_at: string
}

export interface Booking extends GiftActivity {
  share_token?: string
  active: boolean
  can_cancel: boolean
}

export interface HistoryEntry extends GiftActivity {
  gifted: boolean
}

export class APIError extends Error {
  constructor(public readonly status: number, message: string, public readonly confirmRound?: number) {
    super(message)
  }
}

type RequestOptions = {
  method?: 'POST' | 'PUT' | 'DELETE'
  body?: unknown
  signal?: AbortSignal
}

async function requestResponse(path: string, options: RequestOptions = {}) {
  const { method, body, signal } = options
  const form = body instanceof FormData
  const response = await fetch('/api/v1' + path, {
    method,
    signal,
    headers: body === undefined || form ? undefined : { 'Content-Type': 'application/json' },
    body: form ? body : JSON.stringify(body),
  })
  if (!response.ok) {
    const data = await response.json().catch(() => null)
    if (response.status === 401 && path !== '/login') {
      window.dispatchEvent(new Event('wishlist:unauthorized'))
    }
    throw new APIError(response.status, data?.error || 'Не удалось выполнить запрос', data?.confirm_round)
  }
  return response
}

export async function request<T = void>(path: string, options?: RequestOptions): Promise<T> {
  const response = await requestResponse(path, options)
  if (response.status === 204) return undefined as T
  return response.json()
}

export async function requestPage<T>(path: string, cursor: string, signal: AbortSignal) {
  const query = cursor ? (path.includes('?') ? '&' : '?') + 'after=' + encodeURIComponent(cursor) : ''
  const response = await requestResponse(path + query, { signal })
  const data: T = await response.json()
  return { data, next: response.headers.get('X-Next-Cursor') }
}

export function errorMessage(error: unknown) {
  return error instanceof APIError ? error.message : 'Не удалось связаться с сервером. Попробуйте ещё раз.'
}

export const ownerPath = (id: string) => '/wishlists/' + id
export const sharePath = (token: string) => '/shared/' + encodeURIComponent(token)
export const sharedProfilePath = (token: string) => sharePath(token) + '/profile'
export const reservationPath = (token: string, gift: string) => sharePath(token) + '/gifts/' + gift + '/reservation'

export const cancelBookingPath = (gift: string) => '/reservations/' + gift

export const userPath = (id: string) => '/users/' + id
