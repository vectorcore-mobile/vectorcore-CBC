import { useCallback, useState } from 'react'

// Drop-in replacement for useState that also reports whether the setter has
// ever been called by the user, so Add/Edit forms can track "has this been
// touched" without hand-rolling a dirty flag next to every field mutator.
// `reset` re-seeds the value without flipping dirty back on - needed here
// because this codebase reuses one form's state across opens (openAdd()
// calls it with a blank form) rather than remounting a fresh modal per open.
export function useDirtyState(initialValue) {
  const [state, setStateRaw] = useState(initialValue)
  const [dirty, setDirty] = useState(false)

  const setState = useCallback((update) => {
    setDirty(true)
    setStateRaw(update)
  }, [])

  const reset = useCallback((value) => {
    setDirty(false)
    setStateRaw(value)
  }, [])

  return [state, setState, dirty, reset]
}
