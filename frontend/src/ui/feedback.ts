import type {
  DialogApi,
  LoadingBarApi,
  MessageApi,
  NotificationApi,
} from 'naive-ui'

export interface FeedbackApis {
  dialog: DialogApi
  loadingBar: LoadingBarApi
  message: MessageApi
  notification: NotificationApi
}

let feedbackApis: FeedbackApis | null = null

export function installFeedbackApis(apis: FeedbackApis): void {
  feedbackApis = apis
}

export function uninstallFeedbackApis(apis: FeedbackApis): void {
  if (feedbackApis === apis) feedbackApis = null
}

function requireFeedbackApis(): FeedbackApis {
  if (!feedbackApis) {
    throw new Error('Naive UI feedback providers are not mounted')
  }
  return feedbackApis
}

export function useAppDialog(): DialogApi {
  return requireFeedbackApis().dialog
}

export function useAppLoadingBar(): LoadingBarApi {
  return requireFeedbackApis().loadingBar
}

export function useAppMessage(): MessageApi {
  return requireFeedbackApis().message
}

export function useAppNotification(): NotificationApi {
  return requireFeedbackApis().notification
}
