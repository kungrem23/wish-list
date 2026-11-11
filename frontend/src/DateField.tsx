import { useEffect, useId, useRef, useState } from 'react'
import type { ChangeEvent } from 'react'
import { Icon } from './visuals'

function digitsOf(value: string) {
  return value.replace(/\D/g, '')
}

function withDots(digits: string) {
  if (digits.length < 2) return digits
  const month = digits.slice(2, 4)
  return `${digits.slice(0, 2)}.${month}${month.length === 2 ? '.' + digits.slice(4) : ''}`
}

function fromISO(iso: string) {
  const [year, month, day] = iso.split('-')
  return iso ? `${day}.${month}.${year}` : ''
}

function toISO(digits: string) {
  if (digits.length !== 8) return ''
  const year = digits.slice(4)
  if (year === '0000') return ''
  const iso = `${year}-${digits.slice(2, 4)}-${digits.slice(0, 2)}`
  const date = new Date(iso + 'T00:00:00Z')
  return Number.isFinite(date.getTime()) && date.toISOString().slice(0, 10) === iso ? iso : ''
}

export function DateField({ label, name, defaultValue = '', required, max, rangeError = '' }: {
  label: string
  name: string
  defaultValue?: string
  required?: boolean
  max?: string
  rangeError?: string
}) {
  const id = useId()
  const textInput = useRef<HTMLInputElement>(null)
  const picker = useRef<HTMLInputElement>(null)
  const button = useRef<HTMLButtonElement>(null)
  const previous = useRef(fromISO(defaultValue))
  const [showNative, setShowNative] = useState(false)

  useEffect(() => {
    const input = textInput.current!
    if (toISO(digitsOf(input.value))) {
      input.setCustomValidity(picker.current!.validity.rangeOverflow ? rangeError : '')
    }
  }, [max, rangeError])

  useEffect(() => {
    if (!showNative) return
    function dismissOutside(event: PointerEvent) {
      if (event.target !== picker.current && !button.current?.contains(event.target as Node)) {
        setShowNative(false)
      }
    }
    function dismissOnEscape(event: KeyboardEvent) {
      if (event.key === 'Escape' && !event.defaultPrevented) {
        event.preventDefault()
        setShowNative(false)
        button.current?.focus()
      }
    }
    document.addEventListener('pointerdown', dismissOutside, true)
    document.addEventListener('keydown', dismissOnEscape)
    return () => {
      document.removeEventListener('pointerdown', dismissOutside, true)
      document.removeEventListener('keydown', dismissOnEscape)
    }
  }, [showNative])

  function changeText(event: ChangeEvent<HTMLInputElement>) {
    const input = event.currentTarget
    const raw = input.value
    let digits = digitsOf(raw)
    let beforeCaret = digitsOf(raw.slice(0, input.selectionStart ?? raw.length)).length

    // A deleted separator would otherwise reappear without moving the caret.
    if (raw.length < previous.current.length && digits === digitsOf(previous.current)) {
      const { inputType } = event.nativeEvent as InputEvent
      if (inputType === 'deleteContentBackward' && beforeCaret > 0) {
        beforeCaret--
        digits = digits.slice(0, beforeCaret) + digits.slice(beforeCaret + 1)
      } else if (inputType === 'deleteContentForward') {
        digits = digits.slice(0, beforeCaret) + digits.slice(beforeCaret + 1)
      }
    }

    digits = digits.slice(0, 8)
    const formatted = withDots(digits)
    if (raw !== formatted) {
      input.value = formatted
      const caret = withDots(digits.slice(0, beforeCaret)).length
      input.setSelectionRange(caret, caret)
    }
    previous.current = formatted

    const iso = toISO(digits)
    picker.current!.value = iso
    let error = ''
    if (digits && !iso) {
      error = digits.length < 8 ? 'Введите дату целиком в формате ДД.ММ.ГГГГ.' : 'Введите существующую дату.'
    } else if (picker.current!.validity.rangeOverflow) {
      error = rangeError
    }
    input.setCustomValidity(error)
  }

  function openCalendar() {
    try {
      if (picker.current?.showPicker) {
        picker.current.showPicker()
        return
      }
    } catch {
      // Let the user open the calendar directly from the native field.
    }
    setShowNative((visible) => !visible)
  }

  return <div className="date-field">
    <label htmlFor={id}>{label}</label>
    <div className="date-control">
      <input id={id} ref={textInput} type="text" inputMode="numeric" placeholder="ДД.ММ.ГГГГ"
        defaultValue={fromISO(defaultValue)} required={required} maxLength={10} onChange={changeText} />
      <button ref={button} type="button" className="secondary icon-button date-button" aria-label="Выбрать дату в календаре" onClick={openCalendar}>
        <Icon name="calendar" />
      </button>
      <input ref={picker} className="date-picker" type="date" name={name} defaultValue={defaultValue} max={max}
        aria-label={`${label} (календарь)`} aria-hidden={!showNative} tabIndex={showNative ? undefined : -1}
        onChange={(event) => {
          const input = textInput.current!
          previous.current = fromISO(event.currentTarget.value)
          input.value = previous.current
          input.setCustomValidity(event.currentTarget.validity.rangeOverflow ? rangeError : '')
          if (showNative && event.currentTarget.validity.valid) {
            setShowNative(false)
            button.current?.focus()
          }
        }} />
    </div>
  </div>
}
