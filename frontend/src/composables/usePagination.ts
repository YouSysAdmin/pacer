import { computed, ref, type Ref } from 'vue'

export interface Pageable {
  current_page: number
  size: number
  total_pages: number
  total_elements: number
  empty: boolean
}

// The new API returns full arrays (emails use a keyset cursor instead),
// so pagination is a client-side slice over the loaded list.
export function useClientPager<T>(items: Ref<T[]>, size = 20) {
  const page = ref(0)

  const totalPages = computed(() => Math.max(1, Math.ceil(items.value.length / size)))

  const pageable = computed<Pageable>(() => ({
    current_page: Math.min(page.value, totalPages.value - 1),
    size,
    total_pages: totalPages.value,
    total_elements: items.value.length,
    empty: items.value.length === 0,
  }))

  const pageItems = computed(() => {
    const p = pageable.value.current_page
    return items.value.slice(p * size, (p + 1) * size)
  })

  function goToPage(p: number) {
    page.value = Math.min(Math.max(0, p), totalPages.value - 1)
  }

  return { page, pageable, pageItems, goToPage }
}
